package provider

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestValidModelID(t *testing.T) {
	good := []string{"gpt-image-1", "fal-ai/flux/dev", "owner/name:abc123", "gemini-2.5-flash-image", "a@b+c_d.e-f", "higgsfield-ai/soul/v2/standard"}
	for _, id := range good {
		if err := ValidModelID(id); err != nil {
			t.Errorf("%q rejected: %v", id, err)
		}
	}
	bad := []string{
		"", "../x", "a/../b", "a/./b", ".", "..", "/a", "a/", "a//b", "a?b", "a#b", "a%2F..", "a%2f", `a\b`,
		"a b", "a\tb", "a\nb", "a\x00b", "a\x7fb", "é", "a;b", "a:b/../c", strings.Repeat("a", 201),
	}
	for _, id := range bad {
		err := ValidModelID(id)
		if err == nil {
			t.Errorf("%q accepted", id)
		} else if !errors.Is(err, ErrBadModel) {
			t.Errorf("%q: error %v does not wrap ErrBadModel", id, err)
		}
	}
	if err := ValidModelID(strings.Repeat("a", 200)); err != nil {
		t.Errorf("200 chars rejected: %v", err)
	}
}

func TestConfigTimeout(t *testing.T) {
	cases := []struct {
		opt  any
		want time.Duration
		bad  bool
	}{
		{opt: "10m", want: 10 * time.Minute},
		{opt: "90", want: 90 * time.Second},
		{opt: "1.5h", want: 90 * time.Minute},
		{opt: float64(45), want: 45 * time.Second},
		{opt: 30, want: 30 * time.Second},
		{opt: "soon", bad: true},
		{opt: "-5s", bad: true},
		{opt: float64(0), bad: true},
		{opt: "9999h", bad: true},
		{opt: float64(1e12), bad: true},
		{opt: true, bad: true},
	}
	for _, c := range cases {
		got, ok, err := Config{Options: map[string]any{"timeout": c.opt}}.Timeout()
		if c.bad {
			if err == nil {
				t.Errorf("%v: want error", c.opt)
			}
			continue
		}
		if err != nil || !ok || got != c.want {
			t.Errorf("%v: got %v %v %v, want %v", c.opt, got, ok, err, c.want)
		}
	}
	if _, ok, err := (Config{}).Timeout(); ok || err != nil {
		t.Errorf("unset: ok=%v err=%v", ok, err)
	}
}
