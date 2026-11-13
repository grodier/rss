// Package textcut cuts untrusted text to a size, for storing it in Postgres
// text (which must be valid UTF-8, without NUL bytes) or showing it.
package textcut

import (
	"strings"
	"unicode/utf8"
)

// Bytes returns s cut to at most max bytes without splitting a character,
// with each run of invalid UTF-8 and each NUL byte, which Postgres text
// refuses, replaced by U+FFFD. It replaces before cutting, so a replacement
// can't take it past max.
func Bytes(s string, max int) string {
	s = strings.ToValidUTF8(s, "�")
	if strings.IndexByte(s, 0) >= 0 {
		s = strings.ReplaceAll(s, "\x00", "�")
	}
	if len(s) <= max {
		return s
	}
	end := max
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}
