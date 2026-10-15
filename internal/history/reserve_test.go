package history

import (
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReserveConcurrentCannotOverspend(t *testing.T) {
	t.Setenv("IMGEN_BUDGET", "1.00")
	_, path := newStore(t)
	var ok atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { // each goroutine has its own handle, like a separate process
			defer wg.Done()
			if _, err := Open(path).Reserve(0.30, true, time.Now()); err == nil {
				ok.Add(1)
			} else if !errors.Is(err, ErrOverBudget) {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 3 {
		t.Fatalf("granted %d reservations of $0.30 against $1.00, want 3", ok.Load())
	}
}

func TestReserveCountsHistoryAndRelease(t *testing.T) {
	t.Setenv("IMGEN_BUDGET", "1.00")
	s, _ := newStore(t)
	now := time.Now()
	if err := s.Append(Entry{Time: now, CostUSD: 0.60, OK: true}); err != nil {
		t.Fatal(err)
	}
	r, err := s.Reserve(0.30, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reserve(0.30, true, now); !errors.Is(err, ErrOverBudget) {
		t.Fatalf("second reserve: got %v, want ErrOverBudget", err)
	}
	r.Release()
	r.Release() // idempotent
	if _, err := s.Reserve(0.30, true, now); err != nil {
		t.Fatalf("after release: %v", err)
	}
}

func TestReserveUnknownPriceDenied(t *testing.T) {
	t.Setenv("IMGEN_BUDGET", "5")
	s, _ := newStore(t)
	_, err := s.Reserve(0, false, time.Now())
	if !errors.Is(err, ErrOverBudget) {
		t.Fatalf("got %v, want ErrOverBudget", err)
	}
}

func TestReserveNoBudgetAndNilStore(t *testing.T) {
	t.Setenv("IMGEN_BUDGET", "")
	s, _ := newStore(t)
	r, err := s.Reserve(0, false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	r.Release()
	var nilStore *Store
	r, err = nilStore.Reserve(1, true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	r.Release()
}

func TestReserveExpires(t *testing.T) {
	t.Setenv("IMGEN_BUDGET", "1.00")
	s, _ := newStore(t)
	old := time.Now().Add(-2 * reservationTTL)
	if _, err := s.Reserve(0.90, true, old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reserve(0.90, true, time.Now()); err != nil {
		t.Fatalf("stale reservation should not count: %v", err)
	}
}

func TestReserveNilStoreWithBudgetFailsClosed(t *testing.T) {
	t.Setenv("IMGEN_BUDGET", "1")
	var nilStore *Store
	if _, err := nilStore.Reserve(0.1, true, time.Now()); !errors.Is(err, ErrNoStore) {
		t.Fatalf("want ErrNoStore, got %v", err)
	}
}

func TestBudgetActiveAndLedgerPath(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/s")
	t.Setenv("IMGEN_BUDGET", "")
	if BudgetActive() {
		t.Error("active without IMGEN_BUDGET")
	}
	t.Setenv("IMGEN_BUDGET", "2.5")
	if !BudgetActive() || LedgerPath() != "/s/imgen/budget.jsonl" {
		t.Errorf("%v %q", BudgetActive(), LedgerPath())
	}
}

func TestNonFiniteBudgetFailsClosed(t *testing.T) {
	for _, v := range []string{"NaN", "nan", "Inf", "+Inf", "-Inf", "Infinity"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("IMGEN_BUDGET", v)
			if !BudgetActive() {
				t.Fatal("a non-finite budget must stay active, not disable the limit")
			}
			s := Open(filepath.Join(t.TempDir(), "h.jsonl"))
			if _, err := s.Reserve(0.01, true, time.Now()); !errors.Is(err, ErrOverBudget) {
				t.Errorf("Reserve: %v", err)
			}
			if err := s.CheckBudget(0.01, time.Now()); !errors.Is(err, ErrOverBudget) {
				t.Errorf("CheckBudget: %v", err)
			}
		})
	}
}
