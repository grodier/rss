package rss

import (
	"errors"
	"time"
)

// NewsletterAddress is a reader's address for newsletters (a
// newsletter_addresses row): mail to LocalPart@<-newsletter-domain> becomes
// their newsletters. A reader has at most one current address; a retired one
// keeps receiving for NewsletterRetiredFor.
type NewsletterAddress struct {
	ID        string
	UserID    string
	LocalPart string // before the @, e.g. george.k7x2m9
	CreatedAt time.Time
	RetiredAt time.Time // zero while it's the reader's current address
}

// Email is the address at domain, the -newsletter-domain.
func (a NewsletterAddress) Email(domain string) string {
	return a.LocalPart + "@" + domain
}

// NewsletterSiteHost is the host of the one internal site every newsletter
// feed belongs to, made by a migration. It isn't a real host: discovery
// never produces it, and it's never listed, matched, fetched or checked.
const NewsletterSiteHost = "newsletters"

// NewsletterRetiredFor is how long a replaced address keeps receiving, so
// the reader can move their newsletters to the new one.
const NewsletterRetiredFor = 30 * 24 * time.Hour

// NewsletterAddressEvery is how often a reader can get a new address.
const NewsletterAddressEvery = 24 * time.Hour

var (
	// ErrDuplicateAddress is a local part another address already has.
	ErrDuplicateAddress = errors.New("rss: duplicate newsletter address")
	// ErrAddressTooRecent is a new address asked for less than
	// NewsletterAddressEvery after the current one was made.
	ErrAddressTooRecent = errors.New("rss: newsletter address too recent")
	// ErrNoAccess is mail to the address of a reader without
	// FeatureNewsletters.
	ErrNoAccess = errors.New("rss: the reader has no access to newsletters")
	// ErrDuplicateInbound is a received email that's already waiting to be
	// processed for the same address: a replay.
	ErrDuplicateInbound = errors.New("rss: duplicate inbound email")
	// ErrInboundFull is a received email that would take a reader's waiting
	// emails over InboundPendingMaxBytes.
	ErrInboundFull = errors.New("rss: too much inbound email waiting")
)

// InboundMaxBytes is the largest email POST /inbound/email accepts.
const InboundMaxBytes = 5 << 20

// InboundPendingMaxBytes caps the emails waiting to be processed for one
// reader, across their current and retired addresses.
const InboundPendingMaxBytes = 50 << 20

// Email is a received email as the newsletter runner reads it
// (newsletter.Parse): the headers it uses and the body, every value cut to
// a size and valid UTF-8. Addresses are lowercased.
type Email struct {
	FromName  string
	From      string // From's address
	To        []string
	Cc        []string
	ReplyTo   string
	Subject   string
	MessageID string
	Date      time.Time // zero when missing or unreadable

	ListID              string // List-Id's identifier, inside its angle brackets
	ListUnsubscribe     string
	ListUnsubscribePost string

	// AuthenticationResults are the Authentication-Results headers, topmost
	// first. Senders can add their own: newsletter.Authenticate decides
	// which one counts.
	AuthenticationResults []string
	// ARC is the email's ARC sets, by instance (lowest first).
	ARC []ARCSet

	// HTML is the body: the HTML part, or the text part wrapped in
	// paragraphs, without attachments.
	HTML string
	// ForwardedFrom is the sender's address in a forwarded block in the
	// body ("---------- Forwarded message ---------" and the like), when
	// there is one.
	ForwardedFrom string
}

// ARCSet is one instance of an email's ARC headers: who sealed it and the
// authentication results it recorded.
type ARCSet struct {
	Instance int    // i=
	Sealer   string // ARC-Seal's d=, lowercased
	Results  string // ARC-Authentication-Results, after its "i=N;"
}
