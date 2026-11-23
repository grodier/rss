// Package accounts makes the account emails that www and the admin both
// send: the link to confirm an address, the link to reset a password, and
// the notice that an account was deleted. It only makes them: www stores a
// link's token and queues its email with its own stores, and the admin does
// both in one transaction with its audit entry ("Admin" in
// docs/decisions.md).
package accounts

import (
	"time"

	"github.com/grodier/rss/internal/mail"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/token"
)

const (
	// ConfirmLinkTTL is how long a confirmation link works.
	ConfirmLinkTTL = 7 * 24 * time.Hour
	// ResetLinkTTL is how long a password reset link works.
	ResetLinkTTL = time.Hour
)

// Emails makes the account emails from the emails' templates, with links
// on the reader's URL, AppURL (without a trailing slash).
type Emails struct {
	Templates *mail.Templates
	AppURL    string
}

// Link is an email link: the token to store, which holds only its hash, and
// the email that carries it, addressed to the token's address. Name is the
// email's template, for logs.
type Link struct {
	Token rss.Token
	Email mail.Message
	Name  string
}

// Confirmation returns the link that confirms user's address, working for
// ConfirmLinkTTL from now.
func (e Emails) Confirmation(user rss.User, now time.Time) (Link, error) {
	return e.link(user, rss.TokenConfirmEmail, "confirm_email", "/confirm-email", now.Add(ConfirmLinkTTL))
}

// PasswordReset returns the link with which user chooses a new password,
// working for ResetLinkTTL from now.
func (e Emails) PasswordReset(user rss.User, now time.Time) (Link, error) {
	return e.link(user, rss.TokenResetPassword, "reset_password", "/password/reset", now.Add(ResetLinkTTL))
}

// link makes a token for purpose and renders the email name with its link,
// path?token=, to user's address.
func (e Emails) link(user rss.User, purpose, name, path string, expiresAt time.Time) (Link, error) {
	plain, hash, err := token.New()
	if err != nil {
		return Link{}, err
	}
	m, err := e.Templates.Render(name, struct{ Name, URL string }{
		Name: user.Name,
		URL:  e.AppURL + path + "?token=" + plain,
	})
	if err != nil {
		return Link{}, err
	}
	m.To = user.Email
	return Link{
		Token: rss.Token{Hash: hash, UserID: user.ID, Purpose: purpose, Email: user.Email, ExpiresAt: expiresAt},
		Email: m,
		Name:  name,
	}, nil
}

// AccountDeleted returns the email telling user, as they were before their
// account was deleted, that it's gone.
func (e Emails) AccountDeleted(user rss.User) (mail.Message, error) {
	m, err := e.Templates.Render("account_deleted", struct{ Name string }{user.Name})
	if err != nil {
		return mail.Message{}, err
	}
	m.To = user.Email
	return m, nil
}
