package naming

import "testing"

func TestSlug(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"A red cube, on a table!", 40, "red-cube-on-a-table"},
		{"", 40, "image"},
		{"   ", 40, "image"},
		{"!!!", 40, "image"},
		{"the a of", 40, "image"},
		{"Hello   WORLD", 40, "hello-world"},
		{"café au lait", 40, "caf-au-lait"},
		{"日本語", 40, "image"},
		{"a very long prompt about mountains", 12, "very-long-pr"},
		{"sunset over the sea", 11, "sunset-over"},
		{"The cat in the", 40, "cat"},
		{"x", 0, "x"},
	}
	for _, c := range cases {
		if got := Slug(c.in, c.max); got != c.want {
			t.Errorf("Slug(%q,%d)=%q want %q", c.in, c.max, got, c.want)
		}
	}
}

func TestSlugMaxRunes(t *testing.T) {
	got := Slug("abcdefghijklmnopqrstuvwxyz", 5)
	if got != "abcde" {
		t.Fatalf("got %q", got)
	}
}
