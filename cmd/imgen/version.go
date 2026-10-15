package main

import (
	"regexp"
	"runtime/debug"
	"strings"
)

// resolveVersion returns the version set with -ldflags, else the module
// version stamped by "go install module@version", else the short VCS
// revision (with "-dirty" for uncommitted changes), else "dev".
func resolveVersion(ldflags string, info *debug.BuildInfo) string {
	if ldflags != "dev" || info == nil {
		return ldflags
	}
	var rev, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	// A local build stamps a pseudo-version; the revision says more.
	if v := info.Main.Version; v != "" && v != "(devel)" && (rev == "" || !pseudo.MatchString(v)) {
		return strings.TrimPrefix(v, "v")
	}
	if rev == "" {
		return ldflags
	}
	return rev[:min(7, len(rev))] + dirty
}

// pseudo matches the timestamp and revision of a Go pseudo-version.
var pseudo = regexp.MustCompile(`\d{14}-[0-9a-f]{12}`)

// buildVersion is resolveVersion for this binary.
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		info = nil
	}
	return resolveVersion(version, info)
}
