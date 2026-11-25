package rss

import "errors"

var (
	ErrNoRecord       = errors.New("no matching record found")
	ErrNoAccount      = errors.New("no account with this email")
	ErrWrongPassword  = errors.New("wrong password")
	ErrDuplicateEmail = errors.New("duplicate email")
	ErrTooManyEmails  = errors.New("too many emails to this address")
	// ErrAlreadyConfirmed is an account whose address is already confirmed
	// (the admin's Resend confirmation and Mark confirmed).
	ErrAlreadyConfirmed = errors.New("email already confirmed")
	// ErrAddressChanged is an account whose address is no longer the one an
	// admin's action was about (the address its page showed).
	ErrAddressChanged = errors.New("the account's email address changed")
	// ErrOverLimit is emails that no longer fit under the email provider's
	// sending limits (the admin's waves of invites).
	ErrOverLimit = errors.New("over the email sending limits")
	// ErrEmailNotPending is an outbox email already sent or given up on,
	// which the admin's Retry now and Cancel refuse.
	ErrEmailNotPending  = errors.New("email not pending")
	ErrDuplicateTag     = errors.New("duplicate tag")
	ErrTooManyTags      = errors.New("too many tags")
	ErrTooManyTimelines = errors.New("too many timelines")
	ErrOrderChanged     = errors.New("rss: the timelines changed since the page was loaded")
	// ErrInviteUnusable is a signup invite that's unknown, expired, revoked
	// or already used, or a bound invite used with another address.
	ErrInviteUnusable = errors.New("invite can't be used")
)

// Discover's catalog (see Section).
var (
	// ErrUnknownSection is a section ID that isn't one of Discover's
	// sections.
	ErrUnknownSection = errors.New("rss: not one of Discover's sections")
	// ErrFeedGone is a feed that answered 410 Gone, which can't be put in
	// Discover.
	ErrFeedGone = errors.New("rss: the feed is gone")
)

// PickError is the picking page's save failing on one feed: Err is
// ErrUnknownSection, ErrFeedGone or ErrNoRecord, for the feed FeedID.
type PickError struct {
	FeedID string
	Err    error
}

func (e *PickError) Error() string { return "feed " + e.FeedID + ": " + e.Err.Error() }

func (e *PickError) Unwrap() error { return e.Err }
