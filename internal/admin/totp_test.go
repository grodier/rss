package admin

import (
	"bytes"
	"testing"
	"time"
)

// rfcSecret is RFC 6238's SHA-1 test key.
var rfcSecret = []byte("12345678901234567890")

func TestCode(t *testing.T) {
	// RFC 6238, Appendix B: the SHA-1 rows, keeping the last 6 of their 8
	// digits.
	tests := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	}
	for _, tt := range tests {
		if got := Code(rfcSecret, tt.unix/30); got != tt.want {
			t.Errorf("Code(step of %d) = %q, want %q", tt.unix, got, tt.want)
		}
	}
}

func TestVerify(t *testing.T) {
	now := time.Unix(1111111111, 0)
	current := now.Unix() / 30

	tests := []struct {
		name     string
		code     string
		lastStep int64
		wantStep int64
		wantOK   bool
	}{
		{"current step", Code(rfcSecret, current), 0, current, true},
		{"previous step", Code(rfcSecret, current-1), 0, current - 1, true},
		{"next step", Code(rfcSecret, current+1), 0, current + 1, true},
		{"two steps back", Code(rfcSecret, current-2), 0, 0, false},
		{"two steps ahead", Code(rfcSecret, current+2), 0, 0, false},
		{"step already used", Code(rfcSecret, current), current, 0, false},
		{"step before the last used", Code(rfcSecret, current-1), current, 0, false},
		{"next step after the current was used", Code(rfcSecret, current+1), current, current + 1, true},
		{"wrong code", "000000", 0, 0, false},
		{"five digits", Code(rfcSecret, current)[1:], 0, 0, false},
		{"seven digits", Code(rfcSecret, current) + "0", 0, 0, false},
		{"not digits", "12345a", 0, 0, false},
		{"spaces", " 50471", 0, 0, false},
		{"empty", "", 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step, ok := Verify(rfcSecret, tt.code, now, tt.lastStep)
			if ok != tt.wantOK || step != tt.wantStep {
				t.Errorf("Verify(%q, lastStep %d) = %d, %t; want %d, %t", tt.code, tt.lastStep, step, ok, tt.wantStep, tt.wantOK)
			}
		})
	}
}

func TestProvisioningURI(t *testing.T) {
	got := ProvisioningURI("ops+admin@example.com", rfcSecret)
	want := "otpauth://totp/Paper%20Round%20Admin:ops%2Badmin%40example.com?secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ&issuer=Paper%20Round%20Admin"
	if got != want {
		t.Errorf("ProvisioningURI() =\n%s\nwant\n%s", got, want)
	}
}

func TestGenerateSecret(t *testing.T) {
	a, err := GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret() error = %v", err)
	}
	b, err := GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret() error = %v", err)
	}
	if len(a) != 20 || len(b) != 20 {
		t.Errorf("GenerateSecret() lengths = %d, %d; want 20", len(a), len(b))
	}
	if bytes.Equal(a, b) {
		t.Errorf("two calls returned the same secret %x", a)
	}
}
