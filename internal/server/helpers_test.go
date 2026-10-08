package server

import "testing"

func TestSafeReturnPath(t *testing.T) {
	const fallback = "/feeds/abc"
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"local path", "/sites/abc", "/sites/abc"},
		{"query string", "/search?q=x", "/search?q=x"},
		{"fragment", "/sites/abc#feed-x", "/sites/abc#feed-x"},
		{"empty", "", fallback},
		{"relative path", "feeds/x", fallback},
		{"protocol-relative", "//evil.example", fallback},
		{"backslash host", `/\evil.example`, fallback},
		{"absolute URL", "https://evil.example/", fallback},
		{"javascript scheme", "javascript:alert(1)", fallback},
		{"control character", "/sites/abc\n", fallback},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := safeReturnPath(tt.in, fallback); got != tt.want {
				t.Errorf("safeReturnPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
