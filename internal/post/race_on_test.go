//go:build race

package post

// raceEnabled is true under the race detector, which slows pixel loops ~10x.
const raceEnabled = true
