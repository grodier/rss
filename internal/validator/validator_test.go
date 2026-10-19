package validator

import (
	"strings"
	"testing"
)

func TestStringChecks(t *testing.T) {
	emoji30 := strings.Repeat("😀", 30) // 30 chars, 120 bytes

	tests := []struct {
		name string
		got  bool
		want bool
	}{
		{"NotBlank text", NotBlank("a"), true},
		{"NotBlank empty", NotBlank(""), false},
		{"NotBlank spaces", NotBlank("  \t\n"), false},
		{"MinChars enough", MinChars("abcdefgh", 8), true},
		{"MinChars short", MinChars("abcdefg", 8), false},
		{"MinChars multibyte counts chars", MinChars("😀😀", 2), true},
		{"MaxChars at limit", MaxChars("abc", 3), true},
		{"MaxChars over", MaxChars("abcd", 3), false},
		{"MaxChars multibyte", MaxChars(emoji30, 72), true},
		{"Matches valid email", Matches("a@b.co", EmailRX), true},
		{"Matches invalid email", Matches("not-an-email", EmailRX), false},
		{"Matches email missing domain", Matches("a@", EmailRX), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %t, want %t", tt.got, tt.want)
			}
		})
	}
}

func TestValidator(t *testing.T) {
	var v Validator
	if !v.Valid() {
		t.Error("zero Validator should be valid")
	}

	v.CheckField(true, "name", "ignored")
	if !v.Valid() {
		t.Error("passing check should leave Validator valid")
	}

	v.AddFieldError("name", "first")
	v.AddFieldError("name", "second")
	if v.Valid() {
		t.Error("Validator with field error should be invalid")
	}
	if got := v.FieldErrors["name"]; got != "first" {
		t.Errorf("FieldErrors[name] = %q, want %q", got, "first")
	}

	var nf Validator
	nf.AddNonFieldError("oops")
	if nf.Valid() {
		t.Error("Validator with non-field error should be invalid")
	}
}

func TestFirstError(t *testing.T) {
	v := Validator{FieldErrors: map[string]string{"email": "bad", "password": "short"}}
	tests := []struct {
		name   string
		v      Validator
		fields []string
		want   string
	}{
		{"first in the order given", v, []string{"email", "password"}, "email"},
		{"order reversed", v, []string{"password", "email"}, "password"},
		{"skips fields without an error", v, []string{"invite", "name", "password", "email"}, "password"},
		{"no listed field has an error", v, []string{"invite", "name"}, ""},
		{"no errors", Validator{}, []string{"email", "password"}, ""},
		{"no fields", v, nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.v.FirstError(tt.fields...); got != tt.want {
				t.Errorf("FirstError(%q) = %q, want %q", tt.fields, got, tt.want)
			}
		})
	}

	// Templates call it on forms stored by value in an interface, where only
	// value-receiver methods are in the method set.
	var form any = struct{ Validator }{v}
	f, ok := form.(interface{ FirstError(...string) string })
	if !ok {
		t.Fatal("FirstError is not in a Validator value's method set")
	}
	if got := f.FirstError("password"); got != "password" {
		t.Errorf("FirstError on a value = %q, want %q", got, "password")
	}
}

func TestUUIDRX(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"valid lowercase", "123e4567-e89b-12d3-a456-426614174000", true},
		{"valid uppercase", "123E4567-E89B-12D3-A456-426614174000", true},
		{"empty", "", false},
		{"not a uuid", "abc", false},
		{"trailing character", "123e4567-e89b-12d3-a456-426614174000x", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Matches(tt.value, UUIDRX); got != tt.want {
				t.Errorf("Matches(%q, UUIDRX) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}
