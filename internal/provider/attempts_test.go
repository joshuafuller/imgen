package provider

import (
	"errors"
	"fmt"
	"testing"
)

var errRate = errors.New("HTTP 429: slow down. Rate limited after 3 attempts; run it again in 7s")

// Attempted sets Result.Attempts, or wraps the error when the paid request
// was sent more than once. The error text is the cause's, unchanged.
func TestAttempted(t *testing.T) {
	res, err := Attempted(&Result{Model: "m"}, 3, nil)
	if err != nil || res.Attempts != 3 || res.Model != "m" {
		t.Fatalf("res %+v err %v", res, err)
	}
	if res, _ := Attempted(&Result{}, 1, nil); res.Attempts != 1 {
		t.Fatalf("one attempt: %+v", res)
	}
	res, err = Attempted(&Result{Model: "m"}, 2, errRate)
	var ae *AttemptsError
	if res != nil || !errors.As(err, &ae) || ae.Attempts != 2 || !errors.Is(err, errRate) || err.Error() != errRate.Error() {
		t.Fatalf("res %+v err %v", res, err)
	}
	for _, n := range []int{0, 1} {
		if _, err := Attempted(nil, n, errRate); errors.As(err, &ae) || !errors.Is(err, errRate) {
			t.Fatalf("%d attempts: err %v, want the cause unwrapped", n, err)
		}
	}
}

// AttemptsOf reads the count back from an error, through any wrapping; an
// error without one was sent once.
func TestAttemptsOf(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{nil, 1},
		{errRate, 1},
		{&AttemptsError{Attempts: 3, Err: errRate}, 3},
		{fmt.Errorf("gen: %w", &AttemptsError{Attempts: 2, Err: errRate}), 2},
		{&AttemptsError{Attempts: 0, Err: errRate}, 1},
	} {
		if got := AttemptsOf(tc.err); got != tc.want {
			t.Errorf("AttemptsOf(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}
