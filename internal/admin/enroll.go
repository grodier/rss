package admin

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"strings"
	"time"

	"github.com/grodier/rss/internal/password"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/token"
)

// recoveryCodeCount is how many recovery codes enrolling gives an admin.
const recoveryCodeCount = 10

// recoveryEncoding writes recovery codes in lowercase base32, which has no
// 0, 1 or 8 to mistake for O, I or B.
var recoveryEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// ErrWrongCode is Enrollment.Finish's error when the first code doesn't
// match the new secret.
var ErrWrongCode = errors.New("that code didn't work")

// newRecoveryCodes returns recoveryCodeCount codes, "xxxx-xxxx" from 5
// random bytes each, and their hashes, to store: each works once, instead of
// a TOTP code.
func newRecoveryCodes() (codes []string, hashes [][]byte, err error) {
	for range recoveryCodeCount {
		b := make([]byte, 5)
		if _, err := rand.Read(b); err != nil {
			return nil, nil, err
		}
		s := recoveryEncoding.EncodeToString(b)
		code := s[:4] + "-" + s[4:]
		codes = append(codes, code)
		hashes = append(hashes, token.Hash(code))
	}
	return codes, hashes, nil
}

// normalizeRecoveryCode returns code as newRecoveryCodes wrote it, whatever
// its case, spaces and dash, or false if it can't be a recovery code.
func normalizeRecoveryCode(code string) (string, bool) {
	s := strings.ToLower(strings.NewReplacer(" ", "", "-", "").Replace(code))
	if len(s) != 8 {
		return "", false
	}
	if _, err := recoveryEncoding.DecodeString(s); err != nil {
		return "", false
	}
	return s[:4] + "-" + s[4:], true
}

// Enrollment is an admin being enrolled (admin enroll): a new TOTP secret,
// which the admin adds to an authenticator app from URI, and then proves
// with a first code before anything is stored.
type Enrollment struct {
	email    string
	password string
	secret   []byte
}

// NewEnrollment starts enrolling the admin with email and password, which
// the caller has checked, with a new TOTP secret.
func NewEnrollment(email, password string) (*Enrollment, error) {
	secret, err := GenerateSecret()
	if err != nil {
		return nil, err
	}
	return &Enrollment{email: email, password: password, secret: secret}, nil
}

// URI is the provisioning URI an authenticator app reads.
func (e *Enrollment) URI() string {
	return ProvisioningURI(e.email, e.secret)
}

// Finish checks code, the first one the app shows, and if it matches stores
// the admin, in one write with its entry in the audit log: a new one, or
// with reset, an existing one whose password, secret and recovery codes it
// replaces (which logs every admin out). The code's step is stored as the
// last one used, so it can't also log in. It returns the new recovery codes, to show once. ErrWrongCode if code
// doesn't match; rss.ErrDuplicateEmail (new) or rss.ErrNoRecord (reset) if
// the admin exists, or doesn't.
func (e *Enrollment) Finish(ctx context.Context, admins AdminStore, key []byte, code string, reset bool, now time.Time) ([]string, error) {
	step, ok := Verify(e.secret, strings.TrimSpace(code), now, 0)
	if !ok {
		return nil, ErrWrongCode
	}

	hashedPassword, err := password.Hash(e.password)
	if err != nil {
		return nil, err
	}
	sealed, err := Seal(key, e.secret)
	if err != nil {
		return nil, err
	}
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	creds := rss.AdminCredentials{
		HashedPassword:     []byte(hashedPassword),
		SealedTOTPSecret:   sealed,
		TOTPLastStep:       step,
		RecoveryCodeHashes: hashes,
	}

	// Enrolling is only done from the command line.
	by := rss.AuditActor{Via: rss.AuditViaCLI}
	if reset {
		err = admins.Reset(ctx, e.email, creds, by)
	} else {
		_, err = admins.Create(ctx, e.email, creds, by)
	}
	if err != nil {
		return nil, err
	}
	return codes, nil
}
