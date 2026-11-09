package main

import (
	"errors"
	"io"
	"strings"
)

// A QR code encoder, just enough for admin enroll to draw a provisioning URI
// in the terminal: byte mode, error correction level L, versions 1 to 10 (up
// to 271 bytes), following ISO/IEC 18004 as Project Nayuki's reference
// implementation does. Written here rather than added as a dependency (see
// "Admin" in docs/decisions.md).

// qrMaxVersion is the largest version encodeQR makes.
const qrMaxVersion = 10

// Level L's error correction codewords per block and blocks, by version.
var (
	qrECCPerBlock = [qrMaxVersion + 1]int{0, 7, 10, 15, 20, 26, 18, 20, 24, 30, 18}
	qrBlocks      = [qrMaxVersion + 1]int{0, 1, 1, 1, 1, 1, 2, 2, 2, 2, 4}
)

var errQRTooLong = errors.New("too long for a QR code")

// qrCode is a QR code's modules, true for dark, indexed [row][column].
type qrCode [][]bool

// encodeQR returns the smallest QR code, at level L, that holds data.
func encodeQR(data []byte) (qrCode, error) {
	for v := 1; v <= qrMaxVersion; v++ {
		capacity := qrDataCodewords(v)
		// Mode (4 bits), count (8 bits below version 10, else 16), data.
		countBits := 8
		if v >= 10 {
			countBits = 16
		}
		if 4+countBits+8*len(data) > 8*capacity {
			continue
		}
		return newQR(v, qrCodewords(v, countBits, data)), nil
	}
	return nil, errQRTooLong
}

// qrRawModules is how many modules of a version hold codewords and
// remainder bits.
func qrRawModules(v int) int {
	n := (16*v+128)*v + 64
	if v >= 2 {
		align := v/7 + 2
		n -= (25*align-10)*align - 55
		if v >= 7 {
			n -= 36
		}
	}
	return n
}

func qrDataCodewords(v int) int {
	return qrRawModules(v)/8 - qrECCPerBlock[v]*qrBlocks[v]
}

// qrCodewords returns data's codewords for version v: the data bits, padded,
// split into blocks, each followed by its error correction, interleaved.
func qrCodewords(v, countBits int, data []byte) []byte {
	var bits []bool
	put := func(val, n int) {
		for i := n - 1; i >= 0; i-- {
			bits = append(bits, val>>i&1 == 1)
		}
	}
	capacity := qrDataCodewords(v)
	put(0b0100, 4) // byte mode
	put(len(data), countBits)
	for _, b := range data {
		put(int(b), 8)
	}
	put(0, min(4, 8*capacity-len(bits))) // terminator
	put(0, (8-len(bits)%8)%8)
	for pad := 0xEC; len(bits) < 8*capacity; pad ^= 0xEC ^ 0x11 {
		put(pad, 8)
	}
	codewords := make([]byte, capacity)
	for i, b := range bits {
		if b {
			codewords[i/8] |= 1 << (7 - i%8)
		}
	}

	// Short blocks come first; long ones have one more data codeword.
	blocks, ecc := qrBlocks[v], qrECCPerBlock[v]
	raw := qrRawModules(v) / 8
	short := blocks - raw%blocks
	shortLen := raw / blocks
	divisor := rsDivisor(ecc)
	var split [][]byte
	for i, k := 0, 0; i < blocks; i++ {
		n := shortLen - ecc
		if i >= short {
			n++
		}
		block := codewords[k : k+n]
		k += n
		b := append([]byte{}, block...)
		if i < short {
			b = append(b, 0) // a placeholder, skipped when interleaving
		}
		split = append(split, append(b, rsRemainder(block, divisor)...))
	}
	var out []byte
	for i := range split[0] {
		for j, b := range split {
			// The short blocks' placeholder.
			if i == shortLen-ecc && j < short {
				continue
			}
			out = append(out, b[i])
		}
	}
	return out
}

// rsMultiply multiplies in GF(2^8) modulo x^8 + x^4 + x^3 + x^2 + 1.
func rsMultiply(x, y byte) byte {
	var z int
	for i := 7; i >= 0; i-- {
		z = z<<1 ^ (z>>7)*0x11D
		z ^= int(y>>i&1) * int(x)
	}
	return byte(z)
}

// rsDivisor returns the Reed–Solomon generator polynomial of degree n,
// without its leading 1, highest coefficient first.
func rsDivisor(n int) []byte {
	d := make([]byte, n)
	d[n-1] = 1
	root := byte(1)
	for range n {
		for j := range d {
			d[j] = rsMultiply(d[j], root)
			if j+1 < n {
				d[j] ^= d[j+1]
			}
		}
		root = rsMultiply(root, 2)
	}
	return d
}

// rsRemainder returns data's error correction codewords.
func rsRemainder(data, divisor []byte) []byte {
	r := make([]byte, len(divisor))
	for _, b := range data {
		factor := b ^ r[0]
		copy(r, r[1:])
		r[len(r)-1] = 0
		for i, d := range divisor {
			r[i] ^= rsMultiply(d, factor)
		}
	}
	return r
}

// qrBuilder draws a code: its modules, and which are function patterns.
type qrBuilder struct {
	size     int
	modules  qrCode
	function [][]bool
}

func (q *qrBuilder) set(row, col int, dark bool) {
	q.modules[row][col] = dark
	q.function[row][col] = true
}

// newQR draws version v with codewords, choosing the mask with the lowest
// penalty.
func newQR(v int, codewords []byte) qrCode {
	size := 17 + 4*v
	q := &qrBuilder{size: size}
	for range size {
		q.modules = append(q.modules, make([]bool, size))
		q.function = append(q.function, make([]bool, size))
	}

	// Timing patterns, then finders and alignment patterns over them.
	for i := range size {
		q.set(6, i, i%2 == 0)
		q.set(i, 6, i%2 == 0)
	}
	for _, c := range [][2]int{{3, 3}, {3, size - 4}, {size - 4, 3}} {
		for dr := -4; dr <= 4; dr++ {
			for dc := -4; dc <= 4; dc++ {
				r, col := c[0]+dr, c[1]+dc
				if r < 0 || r >= size || col < 0 || col >= size {
					continue
				}
				d := max(abs(dr), abs(dc))
				q.set(r, col, d != 2 && d != 4)
			}
		}
	}
	pos := qrAlignmentPositions(v)
	for i, r := range pos {
		for j, c := range pos {
			last := len(pos) - 1
			if (i == 0 && j == 0) || (i == 0 && j == last) || (i == last && j == 0) {
				continue
			}
			for dr := -2; dr <= 2; dr++ {
				for dc := -2; dc <= 2; dc++ {
					q.set(r+dr, c+dc, max(abs(dr), abs(dc)) != 1)
				}
			}
		}
	}
	q.drawFormat(0) // reserves the format areas; redrawn with the mask
	if v >= 7 {
		rem := v
		for range 12 {
			rem = rem<<1 ^ (rem>>11)*0x1F25
		}
		bits := v<<12 | rem
		for i := range 18 {
			dark := bits>>i&1 == 1
			a, b := size-11+i%3, i/3
			q.set(b, a, dark)
			q.set(a, b, dark)
		}
	}

	// The codewords, in two-column strips from the right, zigzagging up and
	// down, skipping the vertical timing pattern.
	i := 0
	for right := size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for vert := range size {
			for j := range 2 {
				col := right - j
				row := vert
				if (right+1)&2 == 0 {
					row = size - 1 - vert
				}
				if !q.function[row][col] && i < len(codewords)*8 {
					q.modules[row][col] = codewords[i/8]>>(7-i%8)&1 == 1
					i++
				}
			}
		}
	}

	best, bestPenalty := 0, -1
	for mask := range 8 {
		q.applyMask(mask)
		q.drawFormat(mask)
		if p := q.penalty(); bestPenalty < 0 || p < bestPenalty {
			best, bestPenalty = mask, p
		}
		q.applyMask(mask) // undoes it
	}
	q.applyMask(best)
	q.drawFormat(best)
	return q.modules
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// qrAlignmentPositions returns the rows (and columns) of version v's
// alignment patterns' centers.
func qrAlignmentPositions(v int) []int {
	if v == 1 {
		return nil
	}
	n := v/7 + 2
	step := (v*4 + n*2 + 1) / (n*2 - 2) * 2
	pos := make([]int, n)
	pos[0] = 6
	for i, p := n-1, 17+4*v-7; i >= 1; i, p = i-1, p-step {
		pos[i] = p
	}
	return pos
}

// drawFormat draws the format information: level L and mask, twice.
func (q *qrBuilder) drawFormat(mask int) {
	data := 0b01<<3 | mask // level L
	rem := data
	for range 10 {
		rem = rem<<1 ^ (rem>>9)*0x537
	}
	bits := (data<<10 | rem) ^ 0x5412
	bit := func(i int) bool { return bits>>i&1 == 1 }

	for i := range 6 {
		q.set(i, 8, bit(i))
	}
	q.set(7, 8, bit(6))
	q.set(8, 8, bit(7))
	q.set(8, 7, bit(8))
	for i := 9; i < 15; i++ {
		q.set(8, 14-i, bit(i))
	}
	for i := range 8 {
		q.set(8, q.size-1-i, bit(i))
	}
	for i := 8; i < 15; i++ {
		q.set(q.size-15+i, 8, bit(i))
	}
	q.set(q.size-8, 8, true) // the dark module
}

// applyMask flips the data modules mask selects; applying it twice undoes
// it.
func (q *qrBuilder) applyMask(mask int) {
	for y := range q.size {
		for x := range q.size {
			var flip bool
			switch mask {
			case 0:
				flip = (x+y)%2 == 0
			case 1:
				flip = y%2 == 0
			case 2:
				flip = x%3 == 0
			case 3:
				flip = (x+y)%3 == 0
			case 4:
				flip = (x/3+y/2)%2 == 0
			case 5:
				flip = x*y%2+x*y%3 == 0
			case 6:
				flip = (x*y%2+x*y%3)%2 == 0
			case 7:
				flip = ((x+y)%2+x*y%3)%2 == 0
			}
			if flip && !q.function[y][x] {
				q.modules[y][x] = !q.modules[y][x]
			}
		}
	}
}

// penalty scores the code as drawn by the standard's four rules; the mask
// with the lowest score is used.
func (q *qrBuilder) penalty() int {
	n := q.size
	at := func(r, c int, transpose bool) bool {
		if transpose {
			return q.modules[c][r]
		}
		return q.modules[r][c]
	}
	p := 0
	finderLike := []bool{true, false, true, true, true, false, true}
	for _, tr := range []bool{false, true} {
		for r := range n {
			// Runs of five or more modules of one color.
			run := 1
			for c := 1; c <= n; c++ {
				if c < n && at(r, c, tr) == at(r, c-1, tr) {
					run++
					continue
				}
				if run >= 5 {
					p += run - 2
				}
				run = 1
			}
			// Finder-like patterns with four light modules on either side
			// (outside the code counts as light).
			for c := 0; c+7 <= n; c++ {
				match := true
				for k, d := range finderLike {
					if at(r, c+k, tr) != d {
						match = false
						break
					}
				}
				if !match {
					continue
				}
				light := func(from, to int) bool {
					for k := from; k < to; k++ {
						if k >= 0 && k < n && at(r, k, tr) {
							return false
						}
					}
					return true
				}
				if light(c-4, c) || light(c+7, c+11) {
					p += 40
				}
			}
		}
	}
	dark := 0
	for r := range n {
		for c := range n {
			if q.modules[r][c] {
				dark++
			}
			// 2×2 blocks of one color.
			if r+1 < n && c+1 < n {
				m := q.modules[r][c]
				if q.modules[r+1][c] == m && q.modules[r][c+1] == m && q.modules[r+1][c+1] == m {
					p += 3
				}
			}
		}
	}
	// How far the share of dark modules is from half, in steps of 5%.
	total := n * n
	k := (abs(dark*20-total*10)+total-1)/total - 1
	return p + max(k, 0)*10
}

// writeQR draws code in a terminal, two rows of modules per line with half
// blocks, black on white whatever the terminal's colors, inside the
// four-module light margin readers need.
func writeQR(w io.Writer, code qrCode) error {
	const margin = 4
	n := len(code)
	dark := func(r, c int) bool {
		r, c = r-margin, c-margin
		return r >= 0 && r < n && c >= 0 && c < n && code[r][c]
	}
	var b strings.Builder
	for r := 0; r < n+2*margin; r += 2 {
		b.WriteString("\x1b[30;107m") // black on bright white
		for c := range n + 2*margin {
			top, bottom := dark(r, c), dark(r+1, c)
			switch {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		b.WriteString("\x1b[0m\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}
