// Package subenv builds a minimal environment for child processes so secrets
// in the parent environment never reach them.
package subenv

import (
	"os"
	"slices"
	"strings"
)

// base lists the parent variables every child receives.
var base = []string{
	"PATH", "HOME", "USER", "LOGNAME", "LANG", "TZ", "TERM", "TMPDIR", "TEMP", "TMP",
	"XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME",
	"DISPLAY", "WAYLAND_DISPLAY", "DBUS_SESSION_BUS_ADDRESS",
	"WSL_DISTRO_NAME", "WSLENV", "SYSTEMROOT", "WINDIR",
}

// Minimal returns KEY=VALUE pairs for the allowlisted variables, every LC_*
// variable, and the extra names, limited to those set in the parent.
func Minimal(extra ...string) []string {
	var env []string
	seen := map[string]bool{}
	add := func(k string) {
		if seen[k] {
			return
		}
		if v, ok := os.LookupEnv(k); ok {
			seen[k] = true
			env = append(env, k+"="+v)
		}
	}
	for _, k := range base {
		add(k)
	}
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "LC_") {
			add(k)
		}
	}
	for _, k := range slices.Clone(extra) {
		add(k)
	}
	return env
}
