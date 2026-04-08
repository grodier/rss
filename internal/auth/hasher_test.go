package auth

import (
	"strings"
	"testing"
)

func TestArgon2Hasher_RoundTrip(t *testing.T) {
	h := NewArgon2Hasher()
	password := "correct-horse-battery-staple"

	hash, err := h.Hash(password)
	if err != nil {
		t.Fatalf("Hash() error: %v", err)
	}

	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("hash does not start with $argon2id$: %s", hash)
	}

	match, err := h.Compare(password, hash)
	if err != nil {
		t.Fatalf("Compare() error: %v", err)
	}
	if !match {
		t.Fatal("Compare() returned false for correct password")
	}
}

func TestArgon2Hasher_WrongPassword(t *testing.T) {
	h := NewArgon2Hasher()

	hash, err := h.Hash("correct-password")
	if err != nil {
		t.Fatalf("Hash() error: %v", err)
	}

	match, err := h.Compare("wrong-password", hash)
	if err != nil {
		t.Fatalf("Compare() error: %v", err)
	}
	if match {
		t.Fatal("Compare() returned true for wrong password")
	}
}

func TestArgon2Hasher_UniqueSalts(t *testing.T) {
	h := NewArgon2Hasher()
	password := "same-password"

	hash1, err := h.Hash(password)
	if err != nil {
		t.Fatalf("Hash() error: %v", err)
	}

	hash2, err := h.Hash(password)
	if err != nil {
		t.Fatalf("Hash() error: %v", err)
	}

	if hash1 == hash2 {
		t.Fatal("two hashes of the same password should differ (unique salts)")
	}
}
