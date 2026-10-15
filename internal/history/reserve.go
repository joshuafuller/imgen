package history

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// reservationTTL is how long an unreleased reservation counts (a crashed
	// process must not block the budget for ever).
	reservationTTL = 30 * time.Minute
	lockWait       = 10 * time.Second
	lockStale      = 30 * time.Second
)

// ErrNoStore means a budget is set but there is no usable reservation store.
var ErrNoStore = errors.New("a budget is set but no reservation store is available")

// BudgetActive reports whether IMGEN_BUDGET holds a valid budget.
func BudgetActive() bool {
	_, ok := budgetFromEnv()
	return ok
}

// LedgerPath is the cost-only ledger used for the budget when the history is
// off: $XDG_STATE_HOME/imgen/budget.jsonl (next to the history file).
func LedgerPath() string {
	return filepath.Join(filepath.Dir(DefaultPath()), "budget.jsonl")
}

type reservation struct {
	ID     string    `json:"id"`
	Time   time.Time `json:"time"`
	Amount float64   `json:"amount"`
}

// Reservation is spend held against the budget until Release.
type Reservation struct {
	s    *Store
	id   string
	once bool
}

// Reserve holds next USD against IMGEN_BUDGET atomically across goroutines and
// processes: the check and the hold happen under one lock, so parallel calls
// cannot all pass on the same stale total. With a budget set, an unknown price
// (known false) is denied and a nil store fails closed (ErrNoStore). With no
// budget it grants a no-op hold. Call Release after the run, once its real cost is in the history.
func (s *Store) Reserve(next float64, known bool, now time.Time) (*Reservation, error) {
	budget, ok := budgetFromEnv()
	if !ok {
		return &Reservation{}, nil
	}
	if !known {
		return nil, fmt.Errorf("%w: the price of this call is unknown", ErrOverBudget)
	}
	if s == nil {
		return nil, ErrNoStore
	}
	r := &Reservation{s: s}
	err := s.withLock(func() error {
		local := now.Local()
		start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
		spent, _, _, err := s.Spend(start)
		if err != nil {
			return err
		}
		held, err := s.readReservations(now)
		if err != nil {
			return err
		}
		for _, h := range held {
			spent += h.Amount
		}
		if spent+next > budget {
			return fmt.Errorf("%w: spent or held $%.2f of $%.2f today (next call ~$%.2f)", ErrOverBudget, spent, budget, next)
		}
		r.id = uniqueID(now)
		held = append(held, reservation{ID: r.id, Time: now, Amount: next})
		return s.writeReservations(held)
	})
	if err != nil {
		return nil, err
	}
	return r, nil
}

// Release drops the hold. It is safe to call more than once.
func (r *Reservation) Release() {
	if r == nil || r.s == nil || r.once {
		return
	}
	r.once = true
	_ = r.s.withLock(func() error {
		held, err := r.s.readReservations(time.Now())
		if err != nil {
			return err
		}
		kept := held[:0]
		for _, h := range held {
			if h.ID != r.id {
				kept = append(kept, h)
			}
		}
		return r.s.writeReservations(kept)
	})
}

func budgetFromEnv() (float64, bool) {
	raw := strings.TrimSpace(os.Getenv("IMGEN_BUDGET"))
	if raw == "" {
		return 0, false
	}
	b, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}
	if math.IsNaN(b) || math.IsInf(b, 0) {
		return 0, true // comparisons with NaN/Inf pass or misfire, so deny all paid calls instead
	}
	if b < 0 {
		return 0, false
	}
	return b, true
}

func (s *Store) resPath() string  { return s.path + ".reserve" }
func (s *Store) lockPath() string { return s.path + ".lock" }

// withLock runs fn holding the in-process mutex and a cross-process lock file.
func (s *Store) withLock(fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(s.lockPath(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if st, serr := os.Stat(s.lockPath()); serr == nil && time.Since(st.ModTime()) > lockStale {
			_ = os.Remove(s.lockPath()) // a crashed holder
			continue
		}
		if time.Now().After(deadline) {
			return errors.New("history lock busy")
		}
		time.Sleep(5 * time.Millisecond)
	}
	defer func() { _ = os.Remove(s.lockPath()) }()
	return fn()
}

// readReservations returns the holds younger than the TTL. Caller holds the lock.
func (s *Store) readReservations(now time.Time) ([]reservation, error) {
	data, err := os.ReadFile(s.resPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []reservation
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		var r reservation
		if json.Unmarshal(sc.Bytes(), &r) != nil || now.Sub(r.Time) > reservationTTL {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *Store) writeReservations(rs []reservation) error {
	var buf bytes.Buffer
	for _, r := range rs {
		b, _ := json.Marshal(r)
		buf.Write(append(b, '\n'))
	}
	tmp := s.resPath() + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.resPath())
}
