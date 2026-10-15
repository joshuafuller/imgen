// Package history keeps an append-only JSONL log of imgen runs and budget checks.
package history

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrOverBudget is wrapped by CheckBudget when a call would exceed IMGEN_BUDGET.
var ErrOverBudget = errors.New("over budget")

// Entry is one recorded run.
type Entry struct {
	ID        string    `json:"id"`
	Time      time.Time `json:"time"`
	Command   string    `json:"command"`
	Provider  string    `json:"provider"`
	Type      string    `json:"type"`
	Model     string    `json:"model"`
	Prompt    string    `json:"prompt"`
	Args      []string  `json:"args"`
	Files     []string  `json:"files"`
	CostUSD   float64   `json:"cost_usd"`
	CostKnown bool      `json:"cost_known"`
	OK        bool      `json:"ok"`
	Error     string    `json:"error,omitempty"`
}

// Store is a lazy handle on a history file. A nil *Store is a disabled store.
type Store struct {
	path string
	mu   sync.Mutex
}

var errNotFound = errors.New("history entry not found")

// DefaultPath returns $XDG_STATE_HOME/imgen/history.jsonl or ~/.local/state/imgen/history.jsonl.
func DefaultPath() string {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "imgen", "history.jsonl")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "imgen", "history.jsonl")
}

// Open returns a store for path. Nothing is touched until first use.
func Open(path string) *Store { return &Store{path: path} }

func newID(t time.Time) string {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return t.Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

var (
	issuedMu sync.Mutex
	issued   = map[string]bool{}
)

// uniqueID retries newID until it has not been issued by this process.
func uniqueID(t time.Time) string {
	issuedMu.Lock()
	defer issuedMu.Unlock()
	for {
		id := newID(t)
		if !issued[id] {
			issued[id] = true
			return id
		}
	}
}

// Append writes e as one JSONL line, filling ID and Time when empty.
func (s *Store) Append(e Entry) error {
	if s == nil {
		return nil
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	if e.ID == "" {
		e.ID = uniqueID(e.Time)
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(line); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// all reads every valid entry in file order, skipping corrupt lines.
func (s *Store) all() ([]Entry, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.Lock()
	data, err := os.ReadFile(s.path)
	s.mu.Unlock()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []Entry
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e Entry
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// List returns entries newest first, keeping those that pass filter (nil keeps all),
// capped at limit when limit > 0.
func (s *Store) List(limit int, filter func(Entry) bool) ([]Entry, error) {
	es, err := s.all()
	if err != nil || len(es) == 0 {
		return nil, err
	}
	sort.SliceStable(es, func(i, j int) bool { return es[i].Time.After(es[j].Time) })
	var out []Entry
	for _, e := range es {
		if filter != nil && !filter(e) {
			continue
		}
		out = append(out, e)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out, nil
}

// Get finds an entry by exact id, unique id prefix, or "last" (newest OK entry).
func (s *Store) Get(id string) (Entry, error) {
	if id == "" {
		return Entry{}, errors.New("empty history id")
	}
	if id == "last" {
		es, err := s.List(1, func(e Entry) bool { return e.OK })
		if err != nil {
			return Entry{}, err
		}
		if len(es) == 0 {
			return Entry{}, errNotFound
		}
		return es[0], nil
	}
	es, err := s.all()
	if err != nil {
		return Entry{}, err
	}
	var matches []Entry
	for _, e := range es {
		if e.ID == id {
			return e, nil
		}
		if strings.HasPrefix(e.ID, id) {
			matches = append(matches, e)
		}
	}
	switch len(matches) {
	case 0:
		return Entry{}, fmt.Errorf("%w: %q", errNotFound, id)
	case 1:
		return matches[0], nil
	}
	return Entry{}, fmt.Errorf("history id %q is ambiguous (%d matches)", id, len(matches))
}

// Last returns the newest OK entry that has output files.
func (s *Store) Last() (Entry, error) {
	es, err := s.List(1, func(e Entry) bool { return e.OK && len(e.Files) > 0 })
	if err != nil {
		return Entry{}, err
	}
	if len(es) == 0 {
		return Entry{}, errNotFound
	}
	return es[0], nil
}

// Spend totals CostUSD of entries at or after since, grouped by provider and local day.
func (s *Store) Spend(since time.Time) (total float64, byProvider, byDay map[string]float64, err error) {
	byProvider, byDay = map[string]float64{}, map[string]float64{}
	es, err := s.all()
	if err != nil {
		return 0, nil, nil, err
	}
	for _, e := range es {
		if e.Time.Before(since) {
			continue
		}
		total += e.CostUSD
		byProvider[e.Provider] += e.CostUSD
		byDay[e.Time.Local().Format("2006-01-02")] += e.CostUSD
	}
	return total, byProvider, byDay, nil
}

// CheckBudget returns a wrapped ErrOverBudget when spend today plus next exceeds
// IMGEN_BUDGET (USD per local day). Unset or invalid means no limit.
func (s *Store) CheckBudget(next float64, now time.Time) error {
	budget, ok := budgetFromEnv()
	if !ok {
		return nil
	}
	local := now.Local()
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	spent, _, _, err := s.Spend(start)
	if err != nil {
		return err
	}
	if spent+next > budget {
		return fmt.Errorf("%w: spent $%.2f of $%.2f today (next call ~$%.2f)", ErrOverBudget, spent, budget, next)
	}
	return nil
}
