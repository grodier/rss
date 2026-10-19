// Package password hashes and verifies passwords with Argon2id.
package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/text/unicode/norm"
)

// ErrInvalidHash is returned when an encoded hash can't be parsed.
var ErrInvalidHash = errors.New("password: invalid hash")

type params struct {
	memory  uint32 // KiB
	time    uint32
	threads uint8
	saltLen uint32
	keyLen  uint32
}

// current holds the OWASP minimum parameters for Argon2id.
var current = params{
	memory:  19 * 1024,
	time:    2,
	threads: 1,
	saltLen: 16,
	keyLen:  32,
}

// slots caps how many Argon2 computations run at once. Each one allocates
// its memory parameter (19 MiB for current), so without a cap a burst of
// logins or signups could exhaust memory. Callers beyond the cap wait.
var slots = make(chan struct{}, runtime.GOMAXPROCS(0))

// Hash returns an encoded Argon2id hash of pw, normalized to Unicode NFKC
// first so that the same password typed on devices that encode accents
// differently hashes the same.
func Hash(pw string) (string, error) {
	return hashWith(norm.NFKC.String(pw), current)
}

func hashWith(pw string, p params) (string, error) {
	salt := make([]byte, p.saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	slots <- struct{}{}
	key := argon2.IDKey([]byte(pw), salt, p.time, p.memory, p.threads, p.keyLen)
	<-slots

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.memory, p.time, p.threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// Verify reports whether pw, normalized to Unicode NFKC as Hash does, matches
// encoded. A mismatch returns (false, nil). A malformed hash returns
// ErrInvalidHash.
func Verify(pw, encoded string) (bool, error) {
	pw = norm.NFKC.String(pw)
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, ErrInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, ErrInvalidHash
	}
	if version != argon2.Version {
		return false, ErrInvalidHash
	}

	var p params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return false, ErrInvalidHash
	}
	if p.memory == 0 || p.time == 0 || p.threads == 0 {
		return false, ErrInvalidHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, ErrInvalidHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, ErrInvalidHash
	}

	slots <- struct{}{}
	got := argon2.IDKey([]byte(pw), salt, p.time, p.memory, p.threads, uint32(len(want)))
	<-slots
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
