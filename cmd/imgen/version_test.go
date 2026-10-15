package main

import (
	"runtime/debug"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	bi := func(main string, settings ...string) *debug.BuildInfo {
		b := &debug.BuildInfo{Main: debug.Module{Version: main}}
		for i := 0; i+1 < len(settings); i += 2 {
			b.Settings = append(b.Settings, debug.BuildSetting{Key: settings[i], Value: settings[i+1]})
		}
		return b
	}
	for _, tc := range []struct {
		name, ldflags string
		info          *debug.BuildInfo
		want          string
	}{
		{"ldflags win", "0.4.0", bi("v9.9.9"), "0.4.0"},
		{"go install module@version", "dev", bi("v0.4.0"), "0.4.0"},
		{"local build with vcs", "dev", bi("(devel)", "vcs.revision", "0123456789abcdef", "vcs.modified", "false"), "0123456"},
		{"dirty tree", "dev", bi("(devel)", "vcs.revision", "0123456789abcdef", "vcs.modified", "true"), "0123456-dirty"},
		{"local go install stamps a pseudo-version", "dev", bi("v0.0.0-20261015015401-059fe0990067+dirty", "vcs.revision", "059fe0990067abcd", "vcs.modified", "true"), "059fe09-dirty"},
		{"go install module@main", "dev", bi("v0.3.2-0.20261015015401-059fe0990067"), "0.3.2-0.20261015015401-059fe0990067"},
		{"nothing known", "dev", bi("(devel)"), "dev"},
		{"no build info", "dev", nil, "dev"},
	} {
		if got := resolveVersion(tc.ldflags, tc.info); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}
