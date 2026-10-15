package qr

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

// vector is one reference matrix from testdata/gen_vectors.py.
type vector struct {
	Text    string   `json:"text"`
	Level   string   `json:"level"`
	Mask    int      `json:"mask"`
	Version int      `json:"version"`
	Rows    []string `json:"rows"`
}

func loadVectors(t *testing.T) []vector {
	t.Helper()
	b, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vs []vector
	if err := json.Unmarshal(b, &vs); err != nil {
		t.Fatal(err)
	}
	return vs
}

func rows(c *Code) []string {
	out := make([]string, c.Size)
	for y := range c.Size {
		var b strings.Builder
		for x := range c.Size {
			if c.Dark(x, y) {
				b.WriteByte('#')
			} else {
				b.WriteByte('.')
			}
		}
		out[y] = b.String()
	}
	return out
}

func TestEncodeMatchesReference(t *testing.T) {
	for _, v := range loadVectors(t) {
		t.Run(v.Level+"/"+v.Text, func(t *testing.T) {
			lvl, err := ParseLevel(v.Level)
			if err != nil {
				t.Fatal(err)
			}
			c, err := EncodeMask([]byte(v.Text), lvl, v.Mask)
			if err != nil {
				t.Fatal(err)
			}
			if c.Version != v.Version {
				t.Fatalf("version %d, want %d", c.Version, v.Version)
			}
			got := rows(c)
			for y := range got {
				if got[y] != v.Rows[y] {
					t.Fatalf("row %d (mask %d):\n got %s\nwant %s", y, c.Mask, got[y], v.Rows[y])
				}
			}
		})
	}
}

func TestEncodePicksSmallestVersion(t *testing.T) {
	c, err := Encode([]byte("https://github.com/joshuafuller/imgen"), H)
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != 5 || c.Size != 37 {
		t.Fatalf("version %d size %d, want 5 and 37", c.Version, c.Size)
	}
}

func TestEncodeTooLong(t *testing.T) {
	_, err := Encode([]byte(strings.Repeat("z", 120)), H)
	if !errors.Is(err, ErrTooLong) {
		t.Fatalf("err %v, want ErrTooLong", err)
	}
}

func TestEncodeEmpty(t *testing.T) {
	if _, err := Encode(nil, M); err == nil {
		t.Fatal("empty text encoded")
	}
}

func TestParseLevel(t *testing.T) {
	for s, want := range map[string]Level{"L": L, "m": M, "Q": Q, "h": H} {
		if got, err := ParseLevel(s); err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v", s, got, err)
		}
	}
	if _, err := ParseLevel("X"); err == nil {
		t.Error("ParseLevel(X) gave no error")
	}
}

func TestFunctionModules(t *testing.T) {
	c, err := Encode([]byte("hello"), H)
	if err != nil {
		t.Fatal(err)
	}
	// The three finder centers and the timing row are function modules.
	for _, p := range [][2]int{{3, 3}, {c.Size - 4, 3}, {3, c.Size - 4}, {10, 6}, {6, 10}} {
		if c.Kind(p[0], p[1]) == Data {
			t.Errorf("(%d,%d) is data", p[0], p[1])
		}
	}
	if c.Kind(3, 3) != Finder || c.Kind(10, 6) != Timing {
		t.Errorf("kinds %v %v, want finder and timing", c.Kind(3, 3), c.Kind(10, 6))
	}
	if c.Kind(c.Size-1, c.Size-1) != Data {
		t.Error("bottom-right corner is not data")
	}
}
