// Package imglimit holds the image size limits. It has no decoder
// dependencies, so providers can check dimensions without importing imgcheck.
package imglimit

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

const (
	// MaxSide is the largest allowed width or height in pixels.
	MaxSide = 16384
	// DefaultMaxPixels is the default width*height limit; IMGEN_MAX_PIXELS
	// overrides it. 40 MP still admits 8K UHD (7680x4320, 33 MP) and 4K-class output.
	DefaultMaxPixels = 40_000_000
)

var (
	// ErrTooBig means the image exceeds a dimension, pixel or size limit.
	ErrTooBig = errors.New("image too large")
	// ErrBadDims means the dimensions are zero or negative.
	ErrBadDims = errors.New("invalid image dimensions")
)

// MaxPixels returns the active pixel limit.
func MaxPixels() int {
	if s := os.Getenv("IMGEN_MAX_PIXELS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	return DefaultMaxPixels
}

// CheckDims rejects zero, oversized or overflowing dimensions.
func CheckDims(w, h int) error {
	if w <= 0 || h <= 0 {
		return fmt.Errorf("%w: %dx%d", ErrBadDims, w, h)
	}
	if w > MaxSide || h > MaxSide {
		return fmt.Errorf("%w: %dx%d exceeds %d px per side", ErrTooBig, w, h, MaxSide)
	}
	// w and h are each <= MaxSide here, so the product cannot overflow.
	if w*h > MaxPixels() {
		return fmt.Errorf("%w: %dx%d exceeds %d pixels", ErrTooBig, w, h, MaxPixels())
	}
	return nil
}
