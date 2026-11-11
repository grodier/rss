package rss

import "errors"

var (
	ErrNoRecord       = errors.New("no matching record found")
	ErrNoAccount      = errors.New("no account with this email")
	ErrWrongPassword  = errors.New("wrong password")
	ErrDuplicateEmail = errors.New("duplicate email")
	ErrTooManyEmails  = errors.New("too many emails to this address")
	// ErrOverLimit is emails that no longer fit under the email provider's
	// sending limits (the admin's waves of invites).
	ErrOverLimit        = errors.New("over the email sending limits")
	ErrDuplicateTag     = errors.New("duplicate tag")
	ErrTooManyTags      = errors.New("too many tags")
	ErrTooManyTimelines = errors.New("too many timelines")
	ErrOrderChanged     = errors.New("rss: the timelines changed since the page was loaded")
	// ErrInviteUnusable is a signup invite that's unknown, expired, revoked
	// or already used, or a bound invite used with another address.
	ErrInviteUnusable = errors.New("invite can't be used")
)
