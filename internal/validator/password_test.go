package validator

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

func TestCheckNewPassword(t *testing.T) {
	const (
		required = "Password is required"
		tooShort = "Password must be at least 8 characters long"
		tooLong  = "Password must not be more than 256 characters long"
		isEmail  = "Your password can't be your email address."
		common   = "This password is too common. Try a longer one, or a few unrelated words."
	)
	const email = "Ann@Example.com"

	tests := []struct {
		name     string
		password string
		want     string // empty means no error
	}{
		{"blank", "", required},
		{"7 characters", "zq8!Lm2", tooShort},
		{"8 characters", "zq8!Lm2#", ""},
		{"256 characters", strings.Repeat("zq8!Lm2#", 32), ""},
		{"257 characters", strings.Repeat("zq8!Lm2#", 32) + "z", tooLong},
		{"8 multibyte runes", "ééééééé1", ""},
		{"email ignoring case", "ann@example.com", isEmail},
		{"common", "password1", common},
		{"common ignoring case", "PassWord1", common},
		{"entry from the file", commonPasswords()[len(commonPasswords())/2], common},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v Validator
			v.CheckNewPassword("password", tt.password, email)

			if tt.want == "" {
				if !v.Valid() {
					t.Fatalf("FieldErrors = %v, want none", v.FieldErrors)
				}
				return
			}
			if len(v.FieldErrors) != 1 {
				t.Fatalf("FieldErrors = %v, want only %q", v.FieldErrors, "password")
			}
			if got := v.FieldErrors["password"]; got != tt.want {
				t.Errorf("FieldErrors[password] = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCommonPasswordsFile(t *testing.T) {
	lines := commonPasswords()
	if len(lines) == 0 || lines[0] == "" {
		t.Fatal("common_passwords.txt is empty")
	}

	for i, line := range lines {
		if want := strings.ToLower(norm.NFKC.String(line)); line != want {
			t.Errorf("line %d = %q, want it lowercase and NFKC-normalized (%q)", i+1, line, want)
		}
		if n := utf8.RuneCountInString(line); n < 8 || n > 256 {
			t.Errorf("line %d = %q has %d runes, want 8 to 256", i+1, line, n)
		}
		if i > 0 && lines[i-1] == line {
			t.Errorf("line %d = %q is a duplicate", i+1, line)
		}
	}
	if !slices.IsSorted(lines) {
		t.Error("common_passwords.txt isn't sorted bytewise")
	}
}
