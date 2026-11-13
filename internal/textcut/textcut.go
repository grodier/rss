// Package textcut cuts untrusted text to a size, for storing it (Postgres
// text must be valid UTF-8) or showing it.
package textcut

import (
	"strings"
	"unicode/utf8"
)

// Bytes returns s as valid UTF-8, with each invalid sequence replaced by
// U+FFFD, cut to at most max bytes without splitting a character.
func Bytes(s string, max int) string {
	// Cut first, so a large s isn't scanned whole, then cut again if the
	// replacements (three bytes each) took it past max.
	s = strings.ToValidUTF8(cut(s, max), "�")
	return cut(s, max)
}

// cut cuts s to at most max bytes, before the character that would cross
// it.
func cut(s string, max int) string {
	if len(s) <= max {
		return s
	}
	end := max
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}
