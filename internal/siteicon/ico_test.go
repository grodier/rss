package siteicon

import (
	"errors"
	"testing"
)

func TestICOSize(t *testing.T) {
	for _, tt := range []struct {
		name  string
		sizes []int
		want  int
	}{
		{"one entry", []int{48}, 48},
		{"largest of several", []int{16, 64, 32}, 64},
		{"0 means 256", []int{32, 256}, 256},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w, h, err := icoSize(buildICO(t, tt.sizes...))
			if err != nil {
				t.Fatalf("icoSize: %v", err)
			}
			if w != tt.want || h != tt.want {
				t.Errorf("icoSize = %dx%d; want %dx%d", w, h, tt.want, tt.want)
			}
		})
	}

	t.Run("non-square entry", func(t *testing.T) {
		data := buildICO(t, 32)
		data[6], data[7] = 64, 32 // width, height of the first entry
		if w, h, err := icoSize(data); err != nil || w != 64 || h != 32 {
			t.Errorf("icoSize = %dx%d, %v; want 64x32", w, h, err)
		}
	})

	ico := buildICO(t, 16, 32)
	for _, tt := range []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"header only", ico[:6]},
		{"truncated directory", ico[:6+16+5]},
		{"no entries", []byte{0, 0, 1, 0, 0, 0}},
		{"not an ICO", []byte("\x89PNG\r\n\x1a\n0000000000000000000000")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := icoSize(tt.data); !errors.Is(err, errBadICO) {
				t.Errorf("icoSize err = %v; want errBadICO", err)
			}
		})
	}
}
