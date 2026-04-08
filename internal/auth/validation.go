package auth

import (
	"net/mail"
	"strings"
)

// Consider better more consistent way to track validation in future

func ValidateEmail(email string) error {
	addr, err := mail.ParseAddress(email)
	if err != nil {
		return ValidationError{Field: "email", Message: "invalid email address"}
	}
	// Reject "Display Name <addr>" format — only bare addresses are accepted.
	if addr.Address != email {
		return ValidationError{Field: "email", Message: "invalid email address"}
	}
	return nil
}

func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func ValidatePassword(password string) error {
	if len(password) < 8 {
		return ErrPasswordTooShort
	}
	if len(password) > 128 {
		return ErrPasswordTooLong
	}
	return nil
}
