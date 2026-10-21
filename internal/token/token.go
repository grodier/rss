// Package token makes the one-time tokens sent in email links.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// New returns a random token for an email link and the SHA-256 hash to
// store: 32 bytes from crypto/rand, base64url without padding (43 characters).
func New() (plain string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	plain = base64.RawURLEncoding.EncodeToString(b)
	return plain, Hash(plain), nil
}

// Hash returns the SHA-256 hash of a token taken from a link.
func Hash(plain string) []byte {
	h := sha256.Sum256([]byte(plain))
	return h[:]
}
