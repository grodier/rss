package token

import (
	"bytes"
	"regexp"
	"testing"
)

func TestNew(t *testing.T) {
	plain, hash, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(plain) {
		t.Errorf("plain = %q, want 43 URL-safe characters", plain)
	}
	if !bytes.Equal(Hash(plain), hash) {
		t.Errorf("Hash(plain) = %x, want the returned hash %x", Hash(plain), hash)
	}

	plain2, hash2, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if plain2 == plain || bytes.Equal(hash2, hash) {
		t.Errorf("two calls returned the same token %q", plain)
	}
}
