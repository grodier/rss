package admin

import (
	"bytes"
	"testing"
)

func TestSealOpen(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	plaintext := []byte("a TOTP secret, 20 b")

	sealed, err := Seal(key, plaintext)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if bytes.Contains(sealed, plaintext) {
		t.Fatalf("Seal() output contains the plaintext")
	}
	got, err := Open(key, sealed)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("Open() = %q, want %q", got, plaintext)
	}

	again, err := Seal(key, plaintext)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if bytes.Equal(again, sealed) {
		t.Errorf("sealing twice gave the same output; the nonce isn't random")
	}

	wrongKey := bytes.Repeat([]byte{8}, 32)
	changed := bytes.Clone(sealed)
	changed[len(changed)/2] ^= 1
	failures := []struct {
		name       string
		key, input []byte
	}{
		{"wrong key", wrongKey, sealed},
		{"changed byte", key, changed},
		{"truncated", key, sealed[:len(sealed)-1]},
		{"shorter than a nonce", key, sealed[:5]},
	}
	for _, tt := range failures {
		if _, err := Open(tt.key, tt.input); err == nil {
			t.Errorf("Open(%s) error = nil, want an error", tt.name)
		}
	}
}

func TestSealKeySize(t *testing.T) {
	for _, n := range []int{0, 16, 24, 31, 33} {
		key := make([]byte, n)
		if _, err := Seal(key, []byte("x")); err == nil {
			t.Errorf("Seal() with a %d-byte key: error = nil, want an error", n)
		}
		if _, err := Open(key, make([]byte, 64)); err == nil {
			t.Errorf("Open() with a %d-byte key: error = nil, want an error", n)
		}
	}
}
