package validator

import "testing"

func TestAtDomain(t *testing.T) {
	tests := []struct {
		email, domain string
		want          bool
	}{
		{"george.k7x2m9@in.paperround.app", "in.paperround.app", true},
		{"George.K7X2M9@IN.PaperRound.app", "in.paperround.app", true},
		{"a@sub.in.paperround.app", "in.paperround.app", true},
		{"a@paperround.app", "in.paperround.app", false},
		{"a@notin.paperround.app", "in.paperround.app", false},
		{"a@in.paperround.app.example.com", "in.paperround.app", false},
		{"a@in.paperround.app", "", false},
		{"in.paperround.app", "in.paperround.app", false},
	}
	for _, tt := range tests {
		if got := AtDomain(tt.email, tt.domain); got != tt.want {
			t.Errorf("AtDomain(%q, %q) = %v, want %v", tt.email, tt.domain, got, tt.want)
		}
	}
}

func TestCheckNotNewsletterAddress(t *testing.T) {
	var v Validator
	v.CheckNotNewsletterAddress("email", "me@example.com", "in.localhost")
	if !v.Valid() {
		t.Errorf("an ordinary address: %v", v.FieldErrors)
	}
	v.CheckNotNewsletterAddress("email", "me.abc234@in.localhost", "in.localhost")
	if v.FieldErrors["email"] != NewsletterAddressMessage {
		t.Errorf("FieldErrors = %v, want the newsletter message", v.FieldErrors)
	}
}

func TestDomainRX(t *testing.T) {
	for _, ok := range []string{"in.paperround.app", "in.localhost", "a-b.example.com"} {
		if !Matches(ok, DomainRX) {
			t.Errorf("%q doesn't match", ok)
		}
	}
	for _, bad := range []string{"", "localhost", "In.Example.com", "in.example.com.", "-in.example.com", "x@in.example.com", "https://in.example.com", "in..example.com"} {
		if Matches(bad, DomainRX) {
			t.Errorf("%q matches", bad)
		}
	}
}
