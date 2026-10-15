package history

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	d, _ := os.MkdirTemp("", "histhome")
	os.Setenv("HOME", d)
	os.Setenv("XDG_STATE_HOME", filepath.Join(d, "state"))
	os.Unsetenv("IMGEN_BUDGET")
	code := m.Run()
	os.RemoveAll(d)
	os.Exit(code)
}

func newStore(t *testing.T) (*Store, string) {
	p := filepath.Join(t.TempDir(), "sub", "history.jsonl")
	return Open(p), p
}

func TestDefaultPath(t *testing.T) {
	want := filepath.Join(os.Getenv("XDG_STATE_HOME"), "imgen", "history.jsonl")
	if got := DefaultPath(); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	t.Setenv("XDG_STATE_HOME", "")
	want = filepath.Join(os.Getenv("HOME"), ".local", "state", "imgen", "history.jsonl")
	if got := DefaultPath(); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestAppendListModes(t *testing.T) {
	s, p := newStore(t)
	if err := s.Append(Entry{Prompt: "one", OK: true}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file: %v %v", err, fi)
	}
	di, _ := os.Stat(filepath.Dir(p))
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", di.Mode().Perm())
	}
	es, err := s.List(0, nil)
	if err != nil || len(es) != 1 || es[0].ID == "" || es[0].Time.IsZero() {
		t.Fatalf("%v %+v", err, es)
	}
}

func TestIDsUniqueSortable(t *testing.T) {
	s, _ := newStore(t)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		e := Entry{Time: time.Date(2026, 10, 14, 17, 25, 30, 0, time.UTC), OK: true}
		if err := s.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	es, _ := s.List(0, nil)
	for _, e := range es {
		if seen[e.ID] {
			t.Fatalf("dup id %s", e.ID)
		}
		seen[e.ID] = true
		if !strings.HasPrefix(e.ID, "20261014-") && !strings.HasPrefix(e.ID, "20261014") {
			t.Fatalf("id %s", e.ID)
		}
	}
}

func TestIDFormat(t *testing.T) {
	id := newID(time.Date(2026, 10, 14, 17, 25, 30, 0, time.UTC))
	if len(id) != len("20261014-172530-a1b2") || id[:16] != "20261014-172530-" {
		t.Fatalf("%q", id)
	}
}

func TestListNewestFirstLimitFilter(t *testing.T) {
	s, _ := newStore(t)
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		s.Append(Entry{Time: base.Add(time.Duration(i) * time.Hour), Prompt: fmt.Sprint(i), OK: i%2 == 0})
	}
	es, _ := s.List(2, nil)
	if len(es) != 2 || es[0].Prompt != "4" || es[1].Prompt != "3" {
		t.Fatalf("%+v", es)
	}
	es, _ = s.List(0, func(e Entry) bool { return e.OK })
	if len(es) != 3 || es[0].Prompt != "4" {
		t.Fatalf("%+v", es)
	}
}

func TestCorruptLineSkipped(t *testing.T) {
	s, p := newStore(t)
	s.Append(Entry{Prompt: "a", OK: true})
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("{not json\n\n")
	f.Close()
	s.Append(Entry{Prompt: "b", OK: true})
	es, err := s.List(0, nil)
	if err != nil || len(es) != 2 {
		t.Fatalf("%v %+v", err, es)
	}
}

func TestMissingFile(t *testing.T) {
	s, _ := newStore(t)
	es, err := s.List(0, nil)
	if err != nil || len(es) != 0 {
		t.Fatalf("%v %v", err, es)
	}
	if _, err := s.Get("last"); err == nil {
		t.Fatal("want error")
	}
	if _, err := s.Last(); err == nil {
		t.Fatal("want error")
	}
	tot, _, _, err := s.Spend(time.Time{})
	if err != nil || tot != 0 {
		t.Fatal(err)
	}
}

func TestNilStore(t *testing.T) {
	var s *Store
	if err := s.Append(Entry{}); err != nil {
		t.Fatal(err)
	}
	if es, err := s.List(0, nil); err != nil || es != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("x"); err == nil {
		t.Fatal("want err")
	}
	if _, err := s.Last(); err == nil {
		t.Fatal("want err")
	}
	if err := s.CheckBudget(1, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Spend(time.Time{}); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentAppend(t *testing.T) {
	s, p := newStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s2 := Open(p) // separate handles too
			if err := s2.Append(Entry{Prompt: strings.Repeat("x", 3000), OK: true}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	es, _ := s.List(0, nil)
	if len(es) != 40 {
		t.Fatalf("got %d", len(es))
	}
}

func TestGetAndLast(t *testing.T) {
	s, _ := newStore(t)
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	s.Append(Entry{ID: "20261001-000000-aaaa", Time: base, OK: true, Files: []string{"a.png"}})
	s.Append(Entry{ID: "20261001-010000-aabb", Time: base.Add(time.Hour), OK: true, Files: []string{"b.png"}})
	s.Append(Entry{ID: "20261001-020000-cccc", Time: base.Add(2 * time.Hour), OK: false, Error: "boom"})
	s.Append(Entry{ID: "20261001-030000-dddd", Time: base.Add(3 * time.Hour), OK: true})

	e, err := s.Get("last")
	if err != nil || e.ID != "20261001-030000-dddd" {
		t.Fatalf("%v %+v", err, e)
	}
	e, err = s.Last()
	if err != nil || e.ID != "20261001-010000-aabb" {
		t.Fatalf("%v %+v", err, e)
	}
	if e, err = s.Get("20261001-000000-aaaa"); err != nil || e.Files[0] != "a.png" {
		t.Fatalf("%v %+v", err, e)
	}
	if e, err = s.Get("20261001-02"); err != nil || e.ID != "20261001-020000-cccc" {
		t.Fatalf("%v %+v", err, e)
	}
	if _, err = s.Get("20261001-0"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("want ambiguous, got %v", err)
	}
	if _, err = s.Get("nope"); err == nil {
		t.Fatal("want not found")
	}
	if _, err = s.Get(""); err == nil {
		t.Fatal("want error on empty")
	}
}

func TestSpend(t *testing.T) {
	s, _ := newStore(t)
	d1 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)
	d2 := time.Date(2026, 10, 2, 12, 0, 0, 0, time.Local)
	s.Append(Entry{Time: d1, Provider: "gemini", CostUSD: 0.5, OK: true})
	s.Append(Entry{Time: d1, Provider: "openai", CostUSD: 0.25, OK: true})
	s.Append(Entry{Time: d2, Provider: "gemini", CostUSD: 1, OK: true})
	tot, bp, bd, err := s.Spend(d2.Add(-time.Hour))
	if err != nil || tot != 1 || bp["gemini"] != 1 || bd["2026-10-02"] != 1 || len(bd) != 1 {
		t.Fatalf("%v %v %v %v", err, tot, bp, bd)
	}
	tot, bp, bd, _ = s.Spend(time.Time{})
	if tot != 1.75 || bp["gemini"] != 1.5 || bp["openai"] != 0.25 || bd["2026-10-01"] != 0.75 {
		t.Fatalf("%v %v %v", tot, bp, bd)
	}
}

func TestCheckBudget(t *testing.T) {
	s, _ := newStore(t)
	now := time.Date(2026, 10, 14, 15, 0, 0, 0, time.Local)
	s.Append(Entry{Time: now.Add(-time.Hour), CostUSD: 4, OK: true})
	s.Append(Entry{Time: now.Add(-30 * time.Hour), CostUSD: 100, OK: true})

	t.Setenv("IMGEN_BUDGET", "")
	if err := s.CheckBudget(1000, now); err != nil {
		t.Fatal(err)
	}
	t.Setenv("IMGEN_BUDGET", "abc")
	if err := s.CheckBudget(1000, now); err != nil {
		t.Fatal(err)
	}
	t.Setenv("IMGEN_BUDGET", "5")
	if err := s.CheckBudget(1, now); err != nil {
		t.Fatalf("exactly at budget should pass: %v", err)
	}
	err := s.CheckBudget(1.5, now)
	if !errors.Is(err, ErrOverBudget) || !strings.Contains(err.Error(), "spent $4.00 of $5.00 today") {
		t.Fatalf("%v", err)
	}
}
