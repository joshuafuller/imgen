package preview

import (
	"image"
	"image/jpeg"
	"io"
	"strconv"
)

func itoa(n int) string { return strconv.Itoa(n) }

func encodeJPEG(w io.Writer, img image.Image) error { return jpeg.Encode(w, img, nil) }
