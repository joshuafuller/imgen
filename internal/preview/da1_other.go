//go:build !linux

package preview

// queryDA1 is not implemented here: only terminals known by their
// environment variables get sixel.
func queryDA1() bool { return false }
