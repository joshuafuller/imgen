package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/joshuafuller/imgen/internal/history"
	"github.com/joshuafuller/imgen/internal/term"
)

const historyHelp = `usage: imgen history [-n 20] [--json] [--cost [--since 7d]]
List the newest runs (id, time, command, provider, model, cost, prompt).
--cost shows what you spent per provider and per day since --since (Nd or a
Go duration such as 36h; default 7d). Set IMGEN_HISTORY=off to stop recording.`

type costReport struct {
	Since      string             `json:"since"`
	TotalUSD   float64            `json:"total_usd"`
	ByProvider map[string]float64 `json:"by_provider"`
	ByDay      map[string]float64 `json:"by_day"`
}

// parseSince reads "7d", "2w" or a Go duration such as "36h".
func parseSince(s string) (time.Duration, error) {
	if n, ok := strings.CutSuffix(s, "d"); ok {
		d, err := strconv.Atoi(n)
		if err == nil && d >= 0 {
			return time.Duration(d) * 24 * time.Hour, nil
		}
	}
	if n, ok := strings.CutSuffix(s, "w"); ok {
		w, err := strconv.Atoi(n)
		if err == nil && w >= 0 {
			return time.Duration(w) * 7 * 24 * time.Hour, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, usagef("--since wants Nd, Nw or a duration like 36h, got %q", s)
	}
	return d, nil
}

func runHistory(args []string, stdout, stderr io.Writer, env Env) error {
	fs := newFlagSet("history")
	var n int
	var asJSON, cost bool
	var since string
	fs.IntVar(&n, "n", 20, "number of entries")
	fs.BoolVar(&asJSON, "json", false, "JSON output")
	fs.BoolVar(&cost, "cost", false, "show spend instead of entries")
	fs.StringVar(&since, "since", "7d", "with --cost: count back this `AGE`, such as 7d, 2w or 36h")
	pos, err := parseCmd(fs, args, stderr, historyHelp)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usagef("history takes no arguments, got %q", pos[0])
	}
	if cost {
		return printCost(stdout, env, since, asJSON)
	}
	return printEntries(stdout, env, n, asJSON)
}

func printEntries(w io.Writer, env Env, n int, asJSON bool) error {
	es, err := env.store().List(n, nil)
	if err != nil {
		return err
	}
	if asJSON {
		if es == nil {
			es = []history.Entry{}
		}
		b, merr := json.Marshal(es)
		if merr != nil {
			return merr
		}
		fmt.Fprintln(w, string(b))
		return nil
	}
	for _, e := range es {
		status := fmt.Sprintf("$%.3f", e.CostUSD)
		switch {
		case !e.OK:
			status = "failed"
		case !e.CostKnown:
			status = "$?"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", safe(e.ID), safe(e.Command), safe(e.Provider), safe(e.Model), status,
			term.SafeText(oneLine(e.Prompt), 60))
	}
	return nil
}

func printCost(w io.Writer, env Env, since string, asJSON bool) error {
	d, err := parseSince(since)
	if err != nil {
		return err
	}
	from := env.Now().Add(-d)
	total, byProv, byDay, err := env.store().Spend(from)
	if err != nil {
		return err
	}
	if asJSON {
		b, merr := json.Marshal(costReport{Since: from.UTC().Format(time.RFC3339), TotalUSD: total, ByProvider: byProv, ByDay: byDay})
		if merr != nil {
			return merr
		}
		fmt.Fprintln(w, string(b))
		return nil
	}
	fmt.Fprintf(w, "total\t$%.3f\tsince %s\n", total, since)
	printSorted(w, "provider", byProv)
	printSorted(w, "day", byDay)
	return nil
}

func printSorted(w io.Writer, kind string, m map[string]float64) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(w, "%s\t%s\t$%.3f\n", kind, safe(k), m[k])
	}
}
