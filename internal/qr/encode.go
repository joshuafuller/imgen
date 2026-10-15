// Package qr encodes text as a QR code (byte mode, versions 1 to 10) and
// checks a picture of one module by module. It uses only the standard
// library. The encoder follows ISO/IEC 18004; the layout code is close to
// Project Nayuki's reference implementation.
package qr

import (
	"errors"
	"fmt"
	"strings"
)

// Level is the error correction level.
type Level int

// The four error correction levels. H restores about 30% of the codewords.
const (
	L Level = iota
	M
	Q
	H
)

func (l Level) String() string { return [...]string{"L", "M", "Q", "H"}[l] }

// formatBits is the two-bit level code in the format information.
func (l Level) formatBits() int { return [...]int{1, 0, 3, 2}[l] }

// ParseLevel reads L, M, Q or H (any case).
func ParseLevel(s string) (Level, error) {
	switch strings.ToUpper(s) {
	case "L":
		return L, nil
	case "M":
		return M, nil
	case "Q":
		return Q, nil
	case "H":
		return H, nil
	}
	return L, fmt.Errorf("error correction level must be L, M, Q or H, got %q", s)
}

// MaxVersion is the largest version Encode makes (57x57 modules).
const MaxVersion = 10

// ErrTooLong means the text does not fit in a version 10 code at the level.
var ErrTooLong = errors.New("text too long for a QR code of version 10 or less")

// Error correction codewords per block and block count, by level then
// version (index 0 is unused). From ISO/IEC 18004 table 9.
var (
	eccPerBlock = [4][MaxVersion + 1]int{
		{0, 7, 10, 15, 20, 26, 18, 20, 24, 30, 18},
		{0, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26},
		{0, 13, 22, 18, 26, 18, 24, 18, 22, 20, 24},
		{0, 17, 28, 22, 16, 22, 28, 26, 26, 24, 28},
	}
	numBlocks = [4][MaxVersion + 1]int{
		{0, 1, 1, 1, 1, 1, 2, 2, 2, 2, 4},
		{0, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5},
		{0, 1, 1, 2, 2, 4, 4, 6, 6, 8, 8},
		{0, 1, 1, 2, 4, 4, 4, 5, 6, 8, 8},
	}
)

// Kind says what a module is for.
type Kind uint8

// Module kinds. Format covers the format and version information and the
// single dark module.
const (
	Data Kind = iota
	Finder
	Separator
	Timing
	Alignment
	Format
)

func (k Kind) String() string {
	return [...]string{"data", "finder", "separator", "timing", "alignment", "format"}[k]
}

// Code is an encoded QR symbol, without the quiet zone.
type Code struct {
	Version int
	Size    int // modules per side: 17 + 4*Version
	Level   Level
	Mask    int
	dark    []bool
	kind    []Kind
}

// Dark reports whether the module at column x, row y is dark.
func (c *Code) Dark(x, y int) bool { return c.dark[y*c.Size+x] }

// Kind returns the kind of the module at column x, row y.
func (c *Code) Kind(x, y int) Kind { return c.kind[y*c.Size+x] }

// Encode makes the smallest code that holds data at the level, with the mask
// of the lowest penalty.
func Encode(data []byte, lvl Level) (*Code, error) { return EncodeMask(data, lvl, -1) }

// EncodeMask is Encode with a fixed mask 0 to 7; -1 picks the best mask.
func EncodeMask(data []byte, lvl Level, mask int) (*Code, error) {
	if len(data) == 0 {
		return nil, errors.New("nothing to encode")
	}
	if mask < -1 || mask > 7 {
		return nil, fmt.Errorf("mask must be -1 to 7, got %d", mask)
	}
	ver := 0
	for v := 1; v <= MaxVersion; v++ {
		if 4+countBits(v)+8*len(data) <= dataCodewords(v, lvl)*8 {
			ver = v
			break
		}
	}
	if ver == 0 {
		return nil, ErrTooLong
	}
	c := newCode(ver, lvl)
	c.drawCodewords(interleave(dataBits(data, ver, lvl), ver, lvl))
	if mask == -1 {
		best := -1
		for m := range 8 {
			c.applyMask(m)
			c.drawFormat(m)
			if p := c.penalty(); best < 0 || p < best {
				best, mask = p, m
			}
			c.applyMask(m) // XOR undoes it
		}
	}
	c.Mask = mask
	c.applyMask(mask)
	c.drawFormat(mask)
	return c, nil
}

func countBits(ver int) int {
	if ver <= 9 {
		return 8
	}
	return 16
}

// rawModules is the number of modules left for codewords and remainder bits.
func rawModules(ver int) int {
	n := (16*ver+128)*ver + 64
	if ver >= 2 {
		align := ver/7 + 2
		n -= (25*align-10)*align - 55
		if ver >= 7 {
			n -= 36
		}
	}
	return n
}

func dataCodewords(ver int, lvl Level) int {
	return rawModules(ver)/8 - eccPerBlock[lvl][ver]*numBlocks[lvl][ver]
}

// dataBits builds the byte-mode segment, terminator and padding.
func dataBits(data []byte, ver int, lvl Level) []byte {
	var bb bitBuf
	bb.put(4, 4) // byte mode
	bb.put(len(data), countBits(ver))
	for _, b := range data {
		bb.put(int(b), 8)
	}
	capBits := dataCodewords(ver, lvl) * 8
	bb.put(0, min(4, capBits-bb.n))
	bb.put(0, (8-bb.n%8)%8)
	for pad := 0xEC; bb.n < capBits; pad ^= 0xEC ^ 0x11 {
		bb.put(pad, 8)
	}
	return bb.b
}

type bitBuf struct {
	b []byte
	n int
}

func (bb *bitBuf) put(v, bits int) {
	for i := bits - 1; i >= 0; i-- {
		if bb.n%8 == 0 {
			bb.b = append(bb.b, 0)
		}
		if (v>>i)&1 == 1 {
			bb.b[bb.n/8] |= 0x80 >> (bb.n % 8)
		}
		bb.n++
	}
}

// interleave splits data into blocks, adds Reed-Solomon codewords to each and
// interleaves the result.
func interleave(data []byte, ver int, lvl Level) []byte {
	nb, ecLen := numBlocks[lvl][ver], eccPerBlock[lvl][ver]
	raw := rawModules(ver) / 8
	short := nb - raw%nb
	shortLen := raw / nb
	div := rsDivisor(ecLen)
	blocks := make([][]byte, nb)
	k := 0
	for i := range nb {
		n := shortLen - ecLen
		if i >= short {
			n++
		}
		dat := append([]byte(nil), data[k:k+n]...)
		k += n
		ecc := rsRemainder(dat, div)
		if i < short {
			dat = append(dat, 0) // placeholder, skipped below
		}
		blocks[i] = append(dat, ecc...)
	}
	out := make([]byte, 0, raw)
	for i := range blocks[0] {
		for j, b := range blocks {
			if i != shortLen-ecLen || j >= short {
				out = append(out, b[i])
			}
		}
	}
	return out
}

// gfMul multiplies in GF(2^8) modulo x^8 + x^4 + x^3 + x^2 + 1.
func gfMul(x, y byte) byte {
	z := 0
	for i := 7; i >= 0; i-- {
		z = (z << 1) ^ ((z >> 7) * 0x11D)
		z ^= int((y>>i)&1) * int(x)
	}
	return byte(z)
}

func rsDivisor(degree int) []byte {
	res := make([]byte, degree)
	res[degree-1] = 1
	root := byte(1)
	for range degree {
		for j := range res {
			res[j] = gfMul(res[j], root)
			if j+1 < len(res) {
				res[j] ^= res[j+1]
			}
		}
		root = gfMul(root, 2)
	}
	return res
}

func rsRemainder(data, div []byte) []byte {
	res := make([]byte, len(div))
	for _, b := range data {
		f := b ^ res[0]
		copy(res, res[1:])
		res[len(res)-1] = 0
		for i, d := range div {
			res[i] ^= gfMul(d, f)
		}
	}
	return res
}

// newCode draws every function pattern, with the format area reserved.
func newCode(ver int, lvl Level) *Code {
	size := 17 + 4*ver
	c := &Code{Version: ver, Size: size, Level: lvl, dark: make([]bool, size*size), kind: make([]Kind, size*size)}
	for i := range size {
		c.set(6, i, i%2 == 0, Timing)
		c.set(i, 6, i%2 == 0, Timing)
	}
	for _, p := range [][2]int{{3, 3}, {size - 4, 3}, {3, size - 4}} {
		c.drawFinder(p[0], p[1])
	}
	pos := alignPositions(ver)
	for i, y := range pos {
		for j, x := range pos {
			last := len(pos) - 1
			if (i == 0 && j == 0) || (i == 0 && j == last) || (i == last && j == 0) {
				continue
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					c.set(x+dx, y+dy, max(abs(dx), abs(dy)) != 1, Alignment)
				}
			}
		}
	}
	c.drawFormat(0) // reserves the area; the real mask redraws it
	c.drawVersion()
	return c
}

func (c *Code) set(x, y int, dark bool, k Kind) {
	c.dark[y*c.Size+x] = dark
	c.kind[y*c.Size+x] = k
}

func (c *Code) drawFinder(cx, cy int) {
	for dy := -4; dy <= 4; dy++ {
		for dx := -4; dx <= 4; dx++ {
			x, y := cx+dx, cy+dy
			if x < 0 || y < 0 || x >= c.Size || y >= c.Size {
				continue
			}
			d := max(abs(dx), abs(dy))
			k := Finder
			if d == 4 {
				k = Separator
			}
			c.set(x, y, d != 2 && d != 4, k)
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// alignPositions returns the alignment pattern centers on each axis.
func alignPositions(ver int) []int {
	if ver == 1 {
		return nil
	}
	n := ver/7 + 2
	step := (ver*8 + n*3 + 5) / (n*4 - 4) * 2
	out := make([]int, n)
	out[0] = 6
	for i, p := n-1, 17+4*ver-7; i >= 1; i, p = i-1, p-step {
		out[i] = p
	}
	return out
}

// drawFormat writes both copies of the 15-bit format information, plus the
// dark module.
func (c *Code) drawFormat(mask int) {
	data := c.Level.formatBits()<<3 | mask
	rem := data
	for range 10 {
		rem = (rem << 1) ^ ((rem >> 9) * 0x537)
	}
	bits := (data<<10 | rem) ^ 0x5412
	bit := func(i int) bool { return (bits>>i)&1 == 1 }
	for i := range 6 {
		c.set(8, i, bit(i), Format)
	}
	c.set(8, 7, bit(6), Format)
	c.set(8, 8, bit(7), Format)
	c.set(7, 8, bit(8), Format)
	for i := 9; i < 15; i++ {
		c.set(14-i, 8, bit(i), Format)
	}
	for i := range 8 {
		c.set(c.Size-1-i, 8, bit(i), Format)
	}
	for i := 8; i < 15; i++ {
		c.set(8, c.Size-15+i, bit(i), Format)
	}
	c.set(8, c.Size-8, true, Format)
}

// drawVersion writes the two 18-bit version blocks of version 7 and up.
func (c *Code) drawVersion() {
	if c.Version < 7 {
		return
	}
	rem := c.Version
	for range 12 {
		rem = (rem << 1) ^ ((rem >> 11) * 0x1F25)
	}
	bits := c.Version<<12 | rem
	for i := range 18 {
		dark := (bits>>i)&1 == 1
		a, b := c.Size-11+i%3, i/3
		c.set(a, b, dark, Format)
		c.set(b, a, dark, Format)
	}
}

// drawCodewords places the bits in the zigzag order, two columns at a time
// from the bottom right, skipping the vertical timing column.
func (c *Code) drawCodewords(data []byte) {
	i := 0
	for right := c.Size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for vert := range c.Size {
			for j := range 2 {
				x := right - j
				y := vert
				if (right+1)&2 == 0 {
					y = c.Size - 1 - vert // upward
				}
				if c.Kind(x, y) != Data || i >= len(data)*8 {
					continue
				}
				c.dark[y*c.Size+x] = (data[i>>3]>>(7-i&7))&1 == 1
				i++
			}
		}
	}
}

func maskBit(mask, x, y int) bool {
	switch mask {
	case 0:
		return (x+y)%2 == 0
	case 1:
		return y%2 == 0
	case 2:
		return x%3 == 0
	case 3:
		return (x+y)%3 == 0
	case 4:
		return (x/3+y/2)%2 == 0
	case 5:
		return x*y%2+x*y%3 == 0
	case 6:
		return (x*y%2+x*y%3)%2 == 0
	default:
		return ((x+y)%2+x*y%3)%2 == 0
	}
}

func (c *Code) applyMask(mask int) {
	for y := range c.Size {
		for x := range c.Size {
			if c.Kind(x, y) == Data && maskBit(mask, x, y) {
				c.dark[y*c.Size+x] = !c.dark[y*c.Size+x]
			}
		}
	}
}

// penalty scores the symbol by the four mask rules of ISO/IEC 18004 7.8.3.
// Modules outside the symbol count as light.
func (c *Code) penalty() int {
	n := c.Size
	p := 0
	line := make([]bool, n)
	for _, vertical := range []bool{false, true} {
		for a := range n {
			for b := range n {
				if vertical {
					line[b] = c.Dark(a, b)
				} else {
					line[b] = c.Dark(b, a)
				}
			}
			p += runPenalty(line) + finderPenalty(line)
		}
	}
	for y := 0; y+1 < n; y++ {
		for x := 0; x+1 < n; x++ {
			d := c.Dark(x, y)
			if c.Dark(x+1, y) == d && c.Dark(x, y+1) == d && c.Dark(x+1, y+1) == d {
				p += 3
			}
		}
	}
	darkN := 0
	for _, d := range c.dark {
		if d {
			darkN++
		}
	}
	total := n * n
	k := (abs(darkN*20-total*10)+total-1)/total - 1
	return p + k*10
}

// runPenalty scores runs of five or more same-color modules.
func runPenalty(line []bool) int {
	p, run := 0, 1
	for i := 1; i <= len(line); i++ {
		if i < len(line) && line[i] == line[i-1] {
			run++
			continue
		}
		if run >= 5 {
			p += 3 + run - 5
		}
		run = 1
	}
	return p
}

// finderPenalty scores each dark-light-dark-dark-dark-light-dark pattern with
// four light modules before it or after it; both sides count separately.
func finderPenalty(line []bool) int {
	at := func(i int) bool { return i >= 0 && i < len(line) && line[i] }
	light := func(from, to int) bool {
		for i := from; i < to; i++ {
			if at(i) {
				return false
			}
		}
		return true
	}
	core := [7]bool{true, false, true, true, true, false, true}
	p := 0
	for s := 0; s+7 <= len(line); s++ {
		ok := !at(s-1) && !at(s+7)
		for i := 0; ok && i < 7; i++ {
			ok = line[s+i] == core[i]
		}
		if !ok {
			continue
		}
		if light(s-4, s) {
			p += 40
		}
		if light(s+7, s+11) {
			p += 40
		}
	}
	return p
}
