package validator

import (
	"regexp"
	"strings"
)

// DomainRX matches a lowercase host name of two or more labels, like
// in.paperround.app or in.localhost: what -newsletter-domain must be.
var DomainRX = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)

// AtDomain reports whether email is an address at domain or one of its
// subdomains, ignoring case. An empty domain matches nothing.
func AtDomain(email, domain string) bool {
	if domain == "" {
		return false
	}
	_, host, ok := strings.Cut(email, "@")
	if !ok {
		return false
	}
	host = strings.ToLower(host)
	return host == domain || strings.HasSuffix(host, "."+domain)
}

// NewsletterAddressMessage is the error for an account email at the
// newsletter domain.
const NewsletterAddressMessage = "Use your own email address; this one is for newsletters."

// CheckNotNewsletterAddress records an error on field when email is at the
// newsletter domain (-newsletter-domain; "" turns the check off): the app
// never mails a reader's newsletter address, since the domain refuses mail
// from the app and the bounce would count against its sending domain.
func (v *Validator) CheckNotNewsletterAddress(field, email, domain string) {
	v.CheckField(!AtDomain(email, domain), field, NewsletterAddressMessage)
}
