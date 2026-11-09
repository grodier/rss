package admin

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Admins' second factor: TOTP (RFC 6238) with the settings every
// authenticator app assumes, HMAC-SHA-1, 6 digits and 30-second steps, so the
// provisioning URI needn't name them.
const (
	totpStepSeconds = 30
	totpDigits      = 6
	totpIssuer      = "Paper Round Admin"
)

// totpEncoding is base32 without padding, the form authenticator apps expect
// in a provisioning URI.
var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateSecret returns a new TOTP secret: 20 random bytes, HMAC-SHA-1's
// output size, as RFC 4226 recommends.
func GenerateSecret() ([]byte, error) {
	secret := make([]byte, 20)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	return secret, nil
}

// Code returns the 6-digit code for a 30-second step (Unix time / 30): HOTP
// (RFC 4226) with the step as its counter.
func Code(secret []byte, step int64) string {
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	mac := hmac.New(sha1.New, secret)
	mac.Write(counter[:])
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", n%1_000_000)
}

// Verify checks a code against the step now falls in and one step either
// side, for clock drift. It never accepts a step at or before lastStep, the
// last one a code was accepted for, so each code works once. On success it
// returns the matching step, for the caller to store as the new lastStep.
func Verify(secret []byte, code string, now time.Time, lastStep int64) (step int64, ok bool) {
	if len(code) != totpDigits || strings.Trim(code, "0123456789") != "" {
		return 0, false
	}
	current := now.Unix() / totpStepSeconds
	// Every step is compared, in constant time, so the timing doesn't say
	// which one matched.
	for s := current - 1; s <= current+1; s++ {
		match := subtle.ConstantTimeCompare([]byte(Code(secret, s)), []byte(code)) == 1
		if match && s > lastStep && !ok {
			step, ok = s, true
		}
	}
	return step, ok
}

// ProvisioningURI returns the otpauth URI an authenticator app reads, from a
// QR code or pasted, to add the admin's account. The account name is
// escaped for both path and query decoding, so a "+" or space in an email
// reads the same in every app.
func ProvisioningURI(email string, secret []byte) string {
	issuer := strings.ReplaceAll(url.QueryEscape(totpIssuer), "+", "%20")
	account := strings.ReplaceAll(url.QueryEscape(email), "+", "%20")
	return "otpauth://totp/" + issuer + ":" + account +
		"?secret=" + totpEncoding.EncodeToString(secret) +
		"&issuer=" + issuer
}
