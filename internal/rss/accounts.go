package rss

import (
	"strings"
	"time"
)

// SignupMatch is what one of the sites a person named on the waitlist
// matched when they signed up (#640).
type SignupMatch struct {
	Candidate string // as typed
	Site      *Site  // nil when not found
	Feed      *Feed  // the unambiguous feed to follow; nil when the site has several
}

// User is an account.
type User struct {
	ID               string
	Name             string
	Email            string
	EmailConfirmedAt time.Time // zero until the email is confirmed
	Theme            string    // one of the Theme constants; only a Session's User has it
	Layout           string    // LayoutHeadlines or LayoutCards; only a Session's User has it
	CreatedAt        time.Time
	Features         Features // the features the reader has; only a Session's User has them
}

// Themes a user can choose (users.theme). ThemeSystem follows the device's
// light or dark setting.
const (
	ThemeSystem = "system"
	ThemeLight  = "light"
	ThemeDark   = "dark"
)

// Layouts of article rows a user can choose (users.layout): a headline with
// its picture beside it, or a preview card under the feed's avatar.
const (
	LayoutHeadlines = "headlines"
	LayoutCards     = "cards"
)

// Feature is a feature a reader can have beyond the base app, by its name
// in user_features.feature; a user_features row means they have it.
type Feature string

// The features a reader can have.
const (
	// FeatureNewsletters gives a reader an address that turns newsletters
	// into feeds.
	FeatureNewsletters Feature = "newsletters"
)

// Features is a set of Feature values: the features a reader has. Unlike a
// slice it's comparable, so a User stays comparable with ==; the zero value
// is the empty set.
type Features uint8

// featureBits is each Feature's bit in Features.
var featureBits = map[Feature]Features{
	FeatureNewsletters: 1 << 0,
}

// NewFeatures returns the set of fs. A feature it doesn't know is left out:
// during a deploy the database can hold a feature that only the new
// version knows.
func NewFeatures(fs ...Feature) Features {
	var set Features
	for _, f := range fs {
		set |= featureBits[f]
	}
	return set
}

// Has reports whether f is in the set.
func (fs Features) Has(f Feature) bool {
	bit := featureBits[f]
	return bit != 0 && fs&bit != 0
}

// SessionIdleTimeout ends a reader's login (a user_sessions row) that
// hasn't been used for this long; the row is deleted at their next login.
const SessionIdleTimeout = 90 * 24 * time.Hour

// Token is a one-time email link (a user_tokens row).
type Token struct {
	Hash      []byte
	UserID    string
	Purpose   string // TokenConfirmEmail, TokenResetPassword or TokenChangeEmail
	Email     string // the address it was sent to
	ExpiresAt time.Time
}

// Token purposes.
const (
	TokenConfirmEmail  = "confirm_email"
	TokenResetPassword = "reset_password"
	TokenChangeEmail   = "change_email"
)

// WaitlistEntry is someone waiting for an invite (a waitlist row). It
// counts once its address is confirmed.
type WaitlistEntry struct {
	ID    string
	Email string
	Name  string
	Sites string // sites they'd like to follow, as typed, at most 500 characters; "" if not answered
	// Sections are the IDs of the Discover sections they'd like to read, in
	// the sections' order; none if not answered. SectionNames are the names
	// of those that still exist, in the same order, read for the admin.
	Sections     []string
	SectionNames []string
	Source       string    // the ?ref= of the page they joined from; "" if none
	ConfirmedAt  time.Time // zero until the address is confirmed
	CreatedAt    time.Time
}

// SplitSitesAnswer splits a waitlist's sites answer into its parts: by
// commas, semicolons and new lines, trimmed, empty ones dropped, in order.
func SplitSitesAnswer(answer string) []string {
	var parts []string
	for _, p := range strings.FieldsFunc(answer, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	}) {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

// The admin's waitlist tabs: confirmed entries with no pending invite
// (WaitlistWaiting), confirmed entries with one (WaitlistInvited), and
// entries not yet confirmed (WaitlistUnconfirmed).
const (
	WaitlistWaiting     = "waiting"
	WaitlistInvited     = "invited"
	WaitlistUnconfirmed = "unconfirmed"
)

// WaitlistRow is a waitlist entry as the admin lists it, with its latest
// invite (zero, with an empty ID, if it has none).
type WaitlistRow struct {
	WaitlistEntry
	Invite Invite
}

// WaitlistCounts are how many entries each of the admin's waitlist tabs has.
type WaitlistCounts struct {
	Waiting, Invited, Unconfirmed int
}

// Invite is a single-use signup invite (an invites row). A bound invite has
// an Email, and only that address can sign up with it; an open one has none.
type Invite struct {
	ID         string
	Email      string // "" for an open invite
	WaitlistID string // the waitlist entry it was sent to; "" if none, or once the entry is gone
	// Name is the name its waitlist entry gave, to fill in signup's form; ""
	// without an entry. Read only.
	Name       string
	Note       string // who it's for, for open invites; shown only in the admin
	Wave       string // the label of the wave it went out in; "" if none
	ExpiresAt  time.Time
	RedeemedAt time.Time // zero until someone signs up with it
	UserID     string    // the account made with it; "" until then, or once it's deleted
	RevokedAt  time.Time // zero unless revoked
	CreatedAt  time.Time
}

// Invite statuses, from Invite.Status.
const (
	InvitePending  = "pending"
	InviteRedeemed = "redeemed"
	InviteExpired  = "expired"
	InviteRevoked  = "revoked"
)

// Status says whether i can still be used at now: InvitePending if it can,
// else why not. A redeemed invite stays redeemed once it would have expired.
func (i Invite) Status(now time.Time) string {
	switch {
	case !i.RedeemedAt.IsZero():
		return InviteRedeemed
	case !i.RevokedAt.IsZero():
		return InviteRevoked
	case !now.Before(i.ExpiresAt):
		return InviteExpired
	}
	return InvitePending
}

// InviteWave sums up the invites sent in one wave: how many went out, how
// many were used to sign up, and how many of those accounts had a session
// seen 7 or more days after signing up.
type InviteWave struct {
	Label    string
	SentAt   time.Time // its first invite's
	Sent     int
	SignedUp int
	Active   int
}

// Wave is a wave of invites to send from the admin: one per waiting
// waitlist entry, each bound to it, under one label.
type Wave struct {
	Label     string
	ExpiresAt time.Time
	Invites   []WaveInvite
}

// WaveInvite is one invite of a Wave, with its email: the invite's token and
// the entry's new link token (its leave link) are in the email, so both are
// made, and the email rendered, before the wave is sent.
type WaveInvite struct {
	WaitlistID        string
	Email             string
	InviteTokenHash   []byte
	WaitlistTokenHash []byte
	// Subject, Text and HTML are the invite's email.
	Subject, Text, HTML string
}

// Session is one logged-in device (a user_sessions row) with its user.
type Session struct {
	ID         string
	User       User
	LastSeenAt time.Time
}
