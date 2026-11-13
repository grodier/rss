// Package textcut cuts untrusted text to a size, for storing it in Postgres
// text (which must be valid UTF-8, without NUL bytes) or showing it.
package textcut

import (
	"strings"
	"unicode/utf8"
)

// replacement stands in for each run of invalid UTF-8 and each NUL byte.
const replacement = "�"

// Bytes returns s cut to at most max bytes without splitting a character,
// with each run of invalid UTF-8 (as strings.ToValidUTF8 sees runs) and
// each NUL byte, which Postgres text refuses, replaced by U+FFFD. The
// replacements count toward max. It reads s only up to the cut, and copies
// it only when something is replaced.
func Bytes(s string, max int) string {
	var b strings.Builder // the output, once something has been replaced
	replaced := false
	n := 0 // bytes of output so far
	inInvalid := false
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		piece, swap := s[i:i+size], false
		switch {
		case r == utf8.RuneError && size == 1:
			if inInvalid {
				// The rest of a run already replaced.
				i++
				continue
			}
			inInvalid, piece, swap = true, replacement, true
		case r == 0:
			inInvalid, piece, swap = false, replacement, true
		default:
			inInvalid = false
		}
		if n+len(piece) > max {
			break
		}
		if swap && !replaced {
			// Until now the output was s[:i].
			replaced = true
			b.Grow(max)
			b.WriteString(s[:i])
		}
		if replaced {
			b.WriteString(piece)
		}
		n += len(piece)
		i += size
	}
	if replaced {
		return b.String()
	}
	return s[:n]
}
