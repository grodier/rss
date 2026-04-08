package auth

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidCredentials       = errors.New("invalid credentials")
	ErrEmailUnavailable         = errors.New("email address is already in use")
	ErrEmailNotVerified         = errors.New("email address has not been verified")
	ErrIdentityNotFound         = errors.New("identity not found")
	ErrTokenInvalidOrExpired    = errors.New("token is invalid or has expired")
	ErrUserNotFound             = errors.New("user not found")
	ErrCannotRemoveLastEmail    = errors.New("cannot remove the last email address")
	ErrCannotRemovePrimaryEmail = errors.New("cannot remove the primary email address")
	ErrPasswordTooShort         = errors.New("password must be at least 8 characters")
	ErrPasswordTooLong          = errors.New("password must be at most 128 characters")
	ErrSessionNotFound          = errors.New("session not found")
	ErrSessionExpired           = errors.New("session has expired")
	ErrStepUpRequired           = errors.New("step-up authentication required")
	ErrNotAMember               = errors.New("user is not a member of this account")
)

type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}
