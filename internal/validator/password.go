package validator

import (
	_ "embed"
	"slices"
	"strings"
	"sync"

	"golang.org/x/text/unicode/norm"
)

// CheckNewPassword records field's error for a new password: 8 to 256
// characters, not a common password, and not the account's email. Signup,
// "www create-user", password reset and password change use it.
func (v *Validator) CheckNewPassword(field, password, email string) {
	v.CheckField(NotBlank(password), field, "Password is required")
	v.CheckField(MinChars(password, 8), field, "Password must be at least 8 characters long")
	v.CheckField(MaxChars(password, 256), field, "Password must not be more than 256 characters long")
	v.CheckField(!strings.EqualFold(password, email), field, "Your password can't be your email address.")
	v.CheckField(!isCommonPassword(password), field, "This password is too common. Try a longer one, or a few unrelated words.")
}

//go:embed common_passwords.txt
var commonPasswordsFile string

var commonPasswords = sync.OnceValue(func() []string {
	return strings.Split(strings.TrimSuffix(commonPasswordsFile, "\n"), "\n")
})

// isCommonPassword reports whether the NFKC-normalized, lowercased password
// is in common_passwords.txt.
//
// The file is the NCSC's list of the 100,000 most common passwords from Have
// I Been Pwned, as published in SecLists (MIT licence):
// https://github.com/danielmiessler/SecLists/blob/60e43f4d2b0251330ff18ea4fa76aabec0515dcb/Passwords/Common-Credentials/100k-most-used-passwords-NCSC.txt
// It keeps the entries of 8 to 256 characters (shorter ones already fail the
// length check), NFKC-normalized and lowercased like the lookup, deduplicated
// and sorted bytewise so the list can be binary searched. Regenerate it with:
//
//	perl -MUnicode::Normalize -CSD -ne 'chomp; $_ = lc NFKC $_; print "$_\n" if length() >= 8 && length() <= 256' \
//		100k-most-used-passwords-NCSC.txt | LC_ALL=C sort -u > common_passwords.txt
//
// A sorted slice takes a fraction of the memory a map would.
func isCommonPassword(pw string) bool {
	_, found := slices.BinarySearch(commonPasswords(), strings.ToLower(norm.NFKC.String(pw)))
	return found
}
