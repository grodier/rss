package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateEmail_Valid(t *testing.T) {
	valid := []string{
		"user@example.com",
		"alice+tag@example.co.uk",
		"a@b.co",
	}
	for _, email := range valid {
		if err := ValidateEmail(email); err != nil {
			t.Errorf("ValidateEmail(%q) = %v, want nil", email, err)
		}
	}
}

func TestValidateEmail_Invalid(t *testing.T) {
	invalid := []string{
		"",
		"not-an-email",
		"@missing-local.com",
		"missing-domain@",
		"User <user@example.com>",
	}
	for _, email := range invalid {
		if err := ValidateEmail(email); err == nil {
			t.Errorf("ValidateEmail(%q) = nil, want error", email)
		}
	}
}

func TestNormalizeEmail(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"User@Example.COM", "user@example.com"},
		{"  user@example.com  ", "user@example.com"},
		{" Alice@Example.COM ", "alice@example.com"},
	}
	for _, tt := range tests {
		got := NormalizeEmail(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeEmail(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestValidatePassword_TooShort(t *testing.T) {
	err := ValidatePassword(strings.Repeat("a", 7))
	if !errors.Is(err, ErrPasswordTooShort) {
		t.Errorf("ValidatePassword(7 chars) = %v, want ErrPasswordTooShort", err)
	}
}

func TestValidatePassword_MinLength(t *testing.T) {
	err := ValidatePassword(strings.Repeat("a", 8))
	if err != nil {
		t.Errorf("ValidatePassword(8 chars) = %v, want nil", err)
	}
}

func TestValidatePassword_MaxLength(t *testing.T) {
	err := ValidatePassword(strings.Repeat("a", 128))
	if err != nil {
		t.Errorf("ValidatePassword(128 chars) = %v, want nil", err)
	}
}

func TestValidatePassword_TooLong(t *testing.T) {
	err := ValidatePassword(strings.Repeat("a", 129))
	if !errors.Is(err, ErrPasswordTooLong) {
		t.Errorf("ValidatePassword(129 chars) = %v, want ErrPasswordTooLong", err)
	}
}
