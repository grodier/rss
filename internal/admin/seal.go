package admin

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"
)

// Seal encrypts plaintext with AES-256-GCM under a 32-byte key, for secrets
// stored in the database (admins' TOTP secrets) whose key is held only in the
// admin service's environment. The result is a random 12-byte nonce, the
// ciphertext and the authentication tag.
func Seal(key, plaintext []byte) ([]byte, error) {
	aead, err := newSealAEAD(key)
	if err != nil {
		return nil, err
	}
	return aead.Seal(nil, nil, plaintext, nil), nil
}

// Open decrypts what Seal returned under the same key. It fails if the key
// is wrong or the ciphertext was changed or cut short.
func Open(key, ciphertext []byte) ([]byte, error) {
	aead, err := newSealAEAD(key)
	if err != nil {
		return nil, err
	}
	plaintext, err := aead.Open(nil, nil, ciphertext, nil)
	if err != nil {
		return nil, errors.New("admin: open sealed secret: wrong key or damaged ciphertext")
	}
	return plaintext, nil
}

// newSealAEAD returns AES-256-GCM with a random nonce prepended to each
// ciphertext. It refuses AES-128 and AES-192 keys, which aes.NewCipher would
// accept.
func newSealAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("admin: seal key is %d bytes, want 32", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}
