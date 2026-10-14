package validator

import (
	"strings"
	"testing"
)

func TestCheckNewAccount(t *testing.T) {
	const name, email, password = "Ada", "ada@example.com", "correct horse"

	tests := []struct {
		name                      string
		inName, inEmail, inPasswd string
		field, message            string // empty field means no errors
	}{
		{"valid", name, email, password, "", ""},
		{"blank name", " ", email, password, "name", "Name is required"},
		{"blank email", name, "", password, "email", "Email is required"},
		{"invalid email", name, "not-an-email", password, "email", "Email must be a valid email address"},
		{"blank password", name, email, "", "password", "Password is required"},
		{"7-character password", name, email, "1234567", "password", "Password must be at least 8 characters long"},
		{"256-character password", name, email, strings.Repeat("a", 256), "", ""},
		{"257-character password", name, email, strings.Repeat("a", 257), "password", "Password must not be more than 256 characters long"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v Validator
			v.CheckNewAccount(tt.inName, tt.inEmail, tt.inPasswd)

			if tt.field == "" {
				if !v.Valid() {
					t.Fatalf("FieldErrors = %v, want none", v.FieldErrors)
				}
				return
			}
			if len(v.FieldErrors) != 1 {
				t.Fatalf("FieldErrors = %v, want only %q", v.FieldErrors, tt.field)
			}
			if got := v.FieldErrors[tt.field]; got != tt.message {
				t.Errorf("FieldErrors[%q] = %q, want %q", tt.field, got, tt.message)
			}
		})
	}
}
