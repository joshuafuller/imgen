package preview

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	"io"
	"strings"

	"github.com/joshuafuller/imgen/internal/imgcheck"
)

// minCellH is the cell height a sixel frame is sized for when the terminal
// does not report pixels: real cells are rarely smaller, so the frame never
// covers more rows than it reserved (and never scrolls the screen).
const minCellH = 12

// Live redraws the partial pictures of a streamed call in one place on a
// terminal, so the picture sharpens as it renders. The first frame reserves
// rows under the cursor and saves the cursor there (DECSC); each frame
// restores it, replaces the last picture and parks the cursor on the line
// under the area. Live is not safe for concurrent use, and nothing else may
// write to the terminal between Draw and Done.
type Live struct {
	w     io.Writer
	proto protocol
	rows  int // rows reserved by the first frame; 0 before it
	id    int // kitty image id
	drawn bool
}

// NewLive returns a Live for w, or nil when w is not a TTY or the terminal
// has no graphics protocol.
func NewLive(w io.Writer) *Live {
	if !isTTY(w) {
		return nil
	}
	p := detect()
	if p == protoNone {
		return nil
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	return &Live{w: w, proto: p, id: int(binary.BigEndian.Uint32(b[:])>>1) | 1}
}

// Draw shows data in place of the previous frame. The header is checked
// (size, type, pixel limit) before anything decodes the pixels; a rejected
// image writes nothing.
func (l *Live) Draw(data []byte) error {
	if len(data) > imgcheck.MaxInputBytes {
		return fmt.Errorf("preview: %w", imgcheck.ErrTooBig)
	}
	mt, w, h, err := imgcheck.Validate(data, "")
	if err != nil {
		return fmt.Errorf("preview: %w", err)
	}
	if mt == "image/svg+xml" {
		return fmt.Errorf("preview: %w", imgcheck.ErrNotImage)
	}
	pw, ph, rows := l.layout(w, h)
	frame, err := l.frame(data, pw, ph, rows)
	if err != nil {
		return err
	}
	var b strings.Builder
	if l.rows == 0 {
		l.rows = rows
		fmt.Fprintf(&b, "%s\x1b[%dA\r\x1b7", strings.Repeat("\n", rows), rows)
	}
	b.WriteString(l.clear())
	b.WriteString(frame)
	fmt.Fprintf(&b, "\x1b8\x1b[%dB\r", l.rows)
	l.drawn = true
	_, err = io.WriteString(l.w, b.String())
	return err
}

// Done erases the last frame and leaves the cursor where the first frame
// began, so the final picture takes its place. It does nothing before the
// first frame and is safe to call twice.
func (l *Live) Done() {
	if l == nil || !l.drawn {
		return
	}
	s := l.clear()
	l.drawn = false
	_, _ = io.WriteString(l.w, s)
}

// clear rewinds to the saved cursor and removes the last frame: kitty
// pictures live in their own layer and go by id; sixel and iTerm2 pictures
// are cell content and go with an erase (a transparent frame would
// otherwise show the one under it).
func (l *Live) clear() string {
	if !l.drawn {
		return "\x1b8\x1b[J"
	}
	if l.proto == protoKitty {
		return fmt.Sprintf("\x1b8\x1b_Ga=d,d=I,i=%d,q=2\x1b\\\x1b[J", l.id)
	}
	return "\x1b8\x1b[J"
}

// layout fits a w x h image into the terminal width and the reserved rows
// (two rows less than the screen before the first frame). It returns the
// pixel size of a sixel frame and the rows the frame covers.
func (l *Live) layout(w, h int) (pw, ph, rows int) {
	cols, trows, xpix, ypix := size(l.w)
	cw, ch := defaultCellW, defaultCellH
	if xpix > 0 && ypix > 0 && trows > 0 {
		cw, ch = xpix/cols, ypix/trows
	} else if l.proto == protoSixel {
		ch = minCellH
	}
	maxRows := l.rows
	if maxRows == 0 {
		maxRows = 22
		if trows > 2 {
			maxRows = trows - 2
		}
	}
	// Scale up as well as down: kitty and iTerm2 draw at the cell size asked.
	scale := min(float64(cols*cw)/float64(w), float64(maxRows*ch)/float64(h))
	if l.proto == protoSixel {
		scale = min(scale, 1)
	}
	pw, ph = max(1, int(float64(w)*scale+0.5)), max(1, int(float64(h)*scale+0.5))
	rows = min(maxRows, max(1, (ph+ch-1)/ch))
	return pw, ph, rows
}

// frame is the escape sequence of one picture, without a trailing newline.
// The pixels are decoded in full first; kitty and iTerm2 get a new PNG of
// them (see encodePNG).
func (l *Live) frame(data []byte, pw, ph, rows int) (string, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("preview: decode image: %w", err)
	}
	if l.proto == protoSixel {
		return encodeSixel(scaled(img, pw, ph)), nil
	}
	p, err := encodePNG(img)
	if err != nil {
		return "", err
	}
	if l.proto == protoKitty {
		return kittyEscape(p, fmt.Sprintf("a=T,f=100,i=%d,r=%d,C=1,q=2", l.id, rows)), nil
	}
	return fmt.Sprintf("\x1b]1337;File=inline=1;size=%d;height=%d;preserveAspectRatio=1:%s\a",
		len(p), rows, base64.StdEncoding.EncodeToString(p)), nil
}
