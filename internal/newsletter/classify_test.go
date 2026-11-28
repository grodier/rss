package newsletter

import "testing"

func TestSender(t *testing.T) {
	tests := []struct {
		fixture string
		want    string
	}{
		// List-Id's organizational domain is the DKIM and DMARC domain's.
		{"pass.eml", "weekly.example.com"},
		// List-Id from another domain: From.
		{"list-id-other.eml", "news@example.com"},
		// DKIM from an email service, not From's organization.
		{"pass-via.eml", "news@shop.example.org via esp.example.net"},
		// The forwarded block's sender.
		{"manual-forward.eml", "news@news.example.org"},
		// A forward's List-Id is checked against what Google's seal recorded.
		{"forwarded.eml", "weekly.example.com"},
		// Nothing authenticated: From, never List-Id.
		{"fail.eml", "news@example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			e := load(t, tt.fixture)
			if got := Sender(e, Authenticate(e), testReader); got != tt.want {
				t.Errorf("Sender = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSenderPassViaAligned(t *testing.T) {
	e := load(t, "pass-via.eml")
	e.From = "news@esp.example.net"
	if got := Sender(e, PassVia, testReader); got != "news@esp.example.net" {
		t.Errorf("Sender = %q, want From when DKIM shares its organization", got)
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		fixture string
		want    Kind
	}{
		{"from-app.eml", FromApp},
		{"fail.eml", Failed}, // it carries List-Unsubscribe
		{"gmail-forwarding.eml", GmailForwarding},
		{"manual-forward.eml", ManualForward},
		{"forged-manual-forward.eml", Failed},
		{"confirmation.eml", Confirmation}, // it carries List-Unsubscribe
		{"forged-confirmation.eml", Failed},
		{"pass.eml", Newsletter},
		{"forwarded.eml", Newsletter},
		{"other.eml", Other},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			e := load(t, tt.fixture)
			if got := Classify(e, Authenticate(e), testReader); got != tt.want {
				t.Errorf("Classify = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClassifyManualForwardFromSomeoneElse(t *testing.T) {
	e := load(t, "manual-forward.eml")
	if got := Classify(e, Authenticate(e), "someone@else.example"); got != Other {
		t.Errorf("Classify of a forward from another address = %q, want other", got)
	}
}

func TestConfirmLink(t *testing.T) {
	tests := []struct {
		fixture string
		kind    Kind
		want    string
	}{
		// Gmail's confirmation has a link and no code (#869), in a text body.
		{"gmail-forwarding.eml", GmailForwarding, "https://mail-settings.google.com/mail/vf-%5BABC123%5D-cmVhZGVy"},
		// The first link whose text or URL looks like confirming.
		{"confirmation.eml", Confirmation, "https://example.com/s/abc123?t=9"},
		{"pass.eml", Newsletter, ""},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			if got := ConfirmLink(load(t, tt.fixture), tt.kind); got != tt.want {
				t.Errorf("ConfirmLink = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestToReader(t *testing.T) {
	tests := []struct {
		fixture string
		address string
		want    bool
	}{
		{"pass.eml", testNewsletter, true},
		{"pass.eml", "Reader.K7X2M9+news@In.Example.com", true},
		{"alternative.eml", testNewsletter, true},
		// An auto-forward keeps its original To, the reader's own inbox.
		{"forwarded.eml", testNewsletter, false},
		{"pass.eml", "", false},
	}
	for _, tt := range tests {
		if got := ToReader(load(t, tt.fixture), tt.address); got != tt.want {
			t.Errorf("ToReader(%s, %q) = %v, want %v", tt.fixture, tt.address, got, tt.want)
		}
	}
}
