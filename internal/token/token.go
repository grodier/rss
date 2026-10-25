// Package token makes the random tokens in links: one-time tokens sent in
// emails, and share links.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// New returns a random token for an email link (see Random) and the SHA-256
// hash to store.
func New() (plain string, hash []byte, err error) {
	plain, err = Random()
	if err != nil {
		return "", nil, err
	}
	return plain, Hash(plain), nil
}

// Random returns 32 bytes from crypto/rand as base64url without padding
// (43 characters), for tokens in links.
func Random() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Hash returns the SHA-256 hash of a token taken from a link.
func Hash(plain string) []byte {
	h := sha256.Sum256([]byte(plain))
	return h[:]
}
