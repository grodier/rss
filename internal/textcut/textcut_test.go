package textcut

import (
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBytes(t *testing.T) {
	tests := []struct {
		name string
		s    string
		max  int
		want string
	}{
		{"empty", "", 5, ""},
		{"under the limit", "abc", 5, "abc"},
		{"exactly the limit", "abcde", 5, "abcde"},
		{"over the limit", "abcdefgh", 5, "abcde"},
		// "é" is two bytes, so byte 5 is the middle of the third: the cut
		// comes before it.
		{"multibyte at the limit", "aééé", 4, "aé"},
		{"multibyte ending at the limit", "aéé", 5, "aéé"},
		{"invalid UTF-8 under the limit", "a\xffb", 10, "a�b"},
		// The invalid byte becomes three, so the cut must count those:
		// cutting first would return 5 bytes as 7.
		{"invalid UTF-8 over the limit", "ab\xffcd", 5, "ab�"},
		{"zero", "abc", 0, ""},
		// A cut through a valid character drops it rather than leaving a
		// replacement for its first bytes.
		{"emoji across the limit", "ab\U0001F600", 5, "ab"},
		// A run of invalid bytes is one replacement, so text after a long
		// run still fits.
		{"long invalid run", "\xff\xff\xff\xff\xff\xffabc", 6, "\uFFFDabc"},
		{"stray continuation bytes", "error: \x80\x80\x80\x80\x80\x80\x80\x80\x80\x80", 12, "error: \uFFFD"},
		{"NUL bytes", "a\x00b\x00", 10, "a\uFFFDb\uFFFD"},
		// The cut falls inside a replacement: it's dropped whole.
		{"replacement across the limit", "abcd\xff\xffef", 6, "abcd"},
		{"invalid run then NUL", "\xff\xfe\x00a", 10, "\uFFFD\uFFFDa"},
		{"a real U+FFFD is kept", "a\uFFFDb", 10, "a\uFFFDb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Bytes(tt.s, tt.max)
			if got != tt.want {
				t.Errorf("Bytes(%q, %d) = %q, want %q", tt.s, tt.max, got, tt.want)
			}
			if len(got) > tt.max || !utf8.ValidString(got) || strings.IndexByte(got, 0) >= 0 {
				t.Errorf("Bytes(%q, %d) = %q: %d bytes, valid %v; want at most %d valid bytes, no NUL", tt.s, tt.max, got, len(got), utf8.ValidString(got), tt.max)
			}
		})
	}
	if got := Bytes(strings.Repeat("é", 1000), 1001); len(got) != 1000 {
		t.Errorf("1,000 é cut to 1,001 bytes = %d bytes, want 1,000", len(got))
	}
}

// reference is Bytes as plainly as it can be written: replace, then cut.
func reference(s string, max int) string {
	s = strings.ReplaceAll(strings.ToValidUTF8(s, "\uFFFD"), "\x00", "\uFFFD")
	if len(s) <= max {
		return s
	}
	end := max
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}

// TestBytesMatchesReference checks the single pass against reference on
// random text made of the pieces that matter: ASCII, multibyte characters,
// stray and truncated sequences, and NULs.
func TestBytesMatchesReference(t *testing.T) {
	pieces := []string{"a", "z", " ", "é", "€", "\U0001F600", "\uFFFD", "\x00", "\xff", "\x80", "\xe2\x82", "\xf0\x9f\x98"}
	rng := rand.New(rand.NewPCG(1, 2))
	for range 20000 {
		var sb strings.Builder
		for range rng.IntN(12) {
			sb.WriteString(pieces[rng.IntN(len(pieces))])
		}
		s, max := sb.String(), rng.IntN(20)
		if got, want := Bytes(s, max), reference(s, max); got != want {
			t.Fatalf("Bytes(%q, %d) = %q, want %q", s, max, got, want)
		}
	}
}
