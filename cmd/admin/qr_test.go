package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/qr-*.txt")

// qrFixtures are codes checked against testdata/qr-<name>.txt, which were
// decoded with a QR reader (OpenCV's QRCodeDetector) when written: version 1,
// one block; and a provisioning URI's size, version 6, two blocks.
var qrFixtures = []struct{ name, data string }{
	{"v1", "otpauth://totp/x"},
	{"uri", "otpauth://totp/Paper%20Round%20Admin:admin%40example.com?secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ&issuer=Paper%20Round%20Admin"},
}

// art draws code as text, "#" for dark and "." for light.
func art(code qrCode) string {
	var b strings.Builder
	for _, row := range code {
		for _, dark := range row {
			if dark {
				b.WriteByte('#')
			} else {
				b.WriteByte('.')
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func TestEncodeQRFixtures(t *testing.T) {
	for _, tt := range qrFixtures {
		t.Run(tt.name, func(t *testing.T) {
			code, err := encodeQR([]byte(tt.data))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("testdata", "qr-"+tt.name+".txt")
			if *update {
				if err := os.WriteFile(path, []byte(art(code)), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := art(code); got != string(want) {
				t.Errorf("encodeQR(%q) differs from %s:\n%s", tt.data, path, got)
			}
		})
	}
}

func TestEncodeQRVersions(t *testing.T) {
	tests := []struct {
		n, wantSize int
	}{
		{1, 21},   // version 1
		{17, 21},  // version 1's capacity
		{18, 25},  // version 2
		{154, 45}, // version 7's capacity
		{271, 57}, // version 10's capacity
	}
	for _, tt := range tests {
		code, err := encodeQR(bytes.Repeat([]byte("a"), tt.n))
		if err != nil {
			t.Fatalf("encodeQR(%d bytes) error = %v", tt.n, err)
		}
		if len(code) != tt.wantSize || len(code[0]) != tt.wantSize {
			t.Errorf("encodeQR(%d bytes) is %d×%d, want %d×%d", tt.n, len(code), len(code[0]), tt.wantSize, tt.wantSize)
		}
	}
	if _, err := encodeQR(bytes.Repeat([]byte("a"), 272)); err != errQRTooLong {
		t.Errorf("encodeQR(272 bytes) error = %v, want errQRTooLong", err)
	}
}

// TestWriteQR reads the half blocks back into modules.
func TestWriteQR(t *testing.T) {
	code, err := encodeQR([]byte(qrFixtures[1].data))
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := writeQR(&b, code); err != nil {
		t.Fatal(err)
	}
	n := len(code)
	lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	if want := (n + 8 + 1) / 2; len(lines) != want {
		t.Fatalf("wrote %d lines, want %d", len(lines), want)
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, "\x1b[30;107m") || !strings.HasSuffix(line, "\x1b[0m") {
			t.Fatalf("line %d isn't black on white: %q", i, line)
		}
		cells := []rune(strings.TrimSuffix(strings.TrimPrefix(line, "\x1b[30;107m"), "\x1b[0m"))
		if len(cells) != n+8 {
			t.Fatalf("line %d has %d cells, want %d", i, len(cells), n+8)
		}
		for c, cell := range cells {
			for half, r := range []int{2*i - 4, 2*i - 3} {
				col := c - 4
				want := r >= 0 && r < n && col >= 0 && col < n && code[r][col]
				got := cell == '█' || (half == 0 && cell == '▀') || (half == 1 && cell == '▄')
				if got != want {
					t.Fatalf("module (%d, %d) drawn %v, want %v", r, col, got, want)
				}
			}
		}
	}
}
