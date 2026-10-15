// Package pricing estimates the USD cost of an image call from an embedded
// price table, optionally overridden by the file named in IMGEN_PRICES.
package pricing

import (
	_ "embed"
	"encoding/json"
	"os"
	"path"
	"strings"
)

//go:embed prices.json
var embedded []byte

type entry struct {
	Provider      string             `json:"provider"`
	Match         string             `json:"match"`
	Price         float64            `json:"price"`
	Verified      bool               `json:"verified"`
	BySize        map[string]float64 `json:"by_size"`
	ByQuality     map[string]float64 `json:"by_quality"`
	ByQualitySize map[string]float64 `json:"by_quality_size"`
}

type table struct {
	Models []entry `json:"models"`
}

// Estimate returns the estimated USD cost of one call. ok=false when unknown.
func Estimate(providerType, model, size, quality string, n int) (usd float64, ok bool) {
	e := match(load(), providerType, model)
	if e == nil {
		return 0, false
	}
	if n < 1 {
		n = 1
	}
	return e.unit(size, quality) * float64(n), true
}

func (e *entry) unit(size, quality string) float64 {
	q := strings.ToLower(quality)
	s := strings.ToLower(size)
	if v, ok := lookup(e.ByQualitySize, q+":"+s); ok && q != "" && s != "" {
		return v
	}
	if v, ok := lookup(e.ByQuality, q); ok && q != "" {
		return v
	}
	if v, ok := lookup(e.BySize, s); ok && s != "" {
		return v
	}
	return e.Price
}

// lookup finds k in m, ignoring key case.
func lookup(m map[string]float64, k string) (float64, bool) {
	for mk, v := range m {
		if strings.EqualFold(mk, k) {
			return v, true
		}
	}
	return 0, false
}

// match picks the entry with the most specific pattern. Earlier entries win ties.
func match(entries []entry, providerType, model string) *entry {
	var best *entry
	bestScore := -1
	for i := range entries {
		e := &entries[i]
		if e.Provider != "" && e.Provider != providerType {
			continue
		}
		if ok, err := path.Match(e.Match, model); err != nil || !ok {
			continue
		}
		score := len(strings.ReplaceAll(e.Match, "*", ""))*2 + 1
		if !strings.Contains(e.Match, "*") {
			score++
		}
		if score > bestScore {
			best, bestScore = e, score
		}
	}
	return best
}

// load returns override entries first, then embedded ones. A bad or missing
// override file is ignored.
func load() []entry {
	var base table
	_ = json.Unmarshal(embedded, &base)
	p := os.Getenv("IMGEN_PRICES")
	if p == "" {
		return base.Models
	}
	data, err := os.ReadFile(p) //nolint:gosec // path is operator-supplied by design
	if err != nil {
		return base.Models
	}
	var over table
	if json.Unmarshal(data, &over) != nil {
		return base.Models
	}
	return append(over.Models, base.Models...)
}
