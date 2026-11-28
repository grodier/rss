package newsletter

import (
	"slices"
	"testing"
)

func TestAuthenticate(t *testing.T) {
	tests := []struct {
		fixture string
		want    Verdict
	}{
		{"pass.eml", Pass},
		{"fail.eml", Fail},
		// No DMARC policy, DKIM by an email service.
		{"pass-via.eml", PassVia},
		// A policy of p=none that fails is dmarc=fail, not dmarc=none (#869).
		{"p-none-fail.eml", Fail},
		// DKIM broke on the way; Google's seal recorded the original pass.
		{"forwarded.eml", PassForwarded},
		{"forwarded-original-failed.eml", Fail},
		{"forwarded-untrusted-sealer.eml", Fail},
		// A second header with Cloudflare's authserv-id, below Cloudflare's.
		{"forged-below.eml", Fail},
		// A header above Cloudflare's, with another authserv-id.
		{"forged-above.eml", Fail},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			if got := Authenticate(load(t, tt.fixture)); got != tt.want {
				t.Errorf("Authenticate = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAuthenticateWithoutCloudflare(t *testing.T) {
	e := load(t, "pass.eml")
	e.AuthenticationResults = []string{"mx.example.com; dmarc=pass header.from=example.com"}
	if got := Authenticate(e); got != Fail {
		t.Errorf("Authenticate without Cloudflare's header = %q, want fail", got)
	}
}

func TestAuthenticateDMARCMisaligned(t *testing.T) {
	e := load(t, "pass.eml")
	e.From = "news@other.example"
	if got := Authenticate(e); got != Fail {
		t.Errorf("Authenticate with DMARC passing for another domain = %q, want fail", got)
	}
}

func TestParseResults(t *testing.T) {
	id, results := parseResults(`mx.cloudflare.net; dkim=pass (comment; with a semicolon) header.d=Example.com header.s=s1; dmarc=pass header.from=example.com policy.dmarc=none; spf=none smtp.mailfrom="a+caf_=b=c@example.com"; arc=pass`)
	if id != "mx.cloudflare.net" {
		t.Errorf("authserv-id = %q", id)
	}
	var methods []string
	for _, r := range results {
		methods = append(methods, r.method+"="+r.result)
	}
	if want := []string{"dkim=pass", "dmarc=pass", "spf=none", "arc=pass"}; !slices.Equal(methods, want) {
		t.Errorf("results = %q, want %q", methods, want)
	}
	if got := results[0].props["header.d"]; got != "example.com" {
		t.Errorf("header.d = %q", got)
	}
	if got := results[2].props["smtp.mailfrom"]; got != "a+caf_=b=c@example.com" {
		t.Errorf("smtp.mailfrom = %q", got)
	}
}
