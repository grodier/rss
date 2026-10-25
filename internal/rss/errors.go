package rss

import "errors"

var (
	ErrNoRecord       = errors.New("no matching record found")
	ErrNoAccount      = errors.New("no account with this email")
	ErrWrongPassword  = errors.New("wrong password")
	ErrDuplicateEmail = errors.New("duplicate email")
	ErrTooManyEmails  = errors.New("too many emails to this address")
	ErrDuplicateTag   = errors.New("duplicate tag")
	ErrTooManyTags    = errors.New("too many tags")
)
