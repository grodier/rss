package newsletter

import (
	"strings"

	"github.com/grodier/rss/internal/rss"
	"golang.org/x/net/publicsuffix"
)

// AuthServID is the authserv-id of the Authentication-Results header
// Cloudflare Email Routing adds, recorded from real mail (#869; "What real
// mail showed" in docs/decisions.md). Cloudflare's header is the topmost:
// senders' own come below it.
const AuthServID = "mx.cloudflare.net"

// trustedSealers are the domains whose ARC seal Authenticate trusts to
// carry a forwarded email's original verdict: Gmail's and Outlook's.
var trustedSealers = map[string]bool{"google.com": true, "outlook.com": true}

// Verdict is Cloudflare's verdict on an email's sender, as Authenticate
// reads it.
type Verdict string

const (
	// Pass is DMARC passing, aligned with From.
	Pass Verdict = "pass"
	// PassVia is no DMARC policy on From's domain (dmarc=none) and DKIM
	// passing for some domain, as with newsletters sent through an email
	// service.
	PassVia Verdict = "pass_via"
	// PassForwarded is a forward Cloudflare couldn't pass itself, whose ARC
	// chain passed and whose last seal, by Gmail or Outlook, recorded the
	// original sender passing.
	PassForwarded Verdict = "pass_forwarded"
	// Fail is anything else.
	Fail Verdict = "fail"
)

// Authenticate returns Cloudflare's verdict on e's sender. Only the topmost
// Authentication-Results whose authserv-id is AuthServID counts, since
// senders can add their own; signatures are never verified here.
func Authenticate(e rss.Email) Verdict {
	results, ok := cloudflareResults(e)
	if !ok {
		return Fail
	}
	switch {
	case dmarcPass(results, domain(e.From)):
		return Pass
	case dmarcNone(results) && len(dkimPassDomains(results)) > 0:
		return PassVia
	case resultOf(results, "arc") == "pass":
		if sealed, ok := lastSeal(e); ok && trustedSealers[sealed.sealer] && originalPass(sealed.results, domain(e.From)) {
			return PassForwarded
		}
	}
	return Fail
}

// authResult is one method's result in an Authentication-Results header:
// "dkim=pass header.d=example.com" is method dkim, result pass and the
// property header.d.
type authResult struct {
	method, result string
	props          map[string]string
}

// parseResults reads an Authentication-Results value: its authserv-id, then
// its results. Comments in parentheses are dropped.
func parseResults(v string) (string, []authResult) {
	parts := strings.Split(stripComments(v), ";")
	fields := strings.Fields(parts[0])
	if len(fields) == 0 {
		return "", nil
	}
	var results []authResult
	for _, p := range parts[1:] {
		fs := strings.Fields(p)
		if len(fs) == 0 {
			continue
		}
		m, res, ok := strings.Cut(fs[0], "=")
		if !ok {
			continue
		}
		r := authResult{method: strings.ToLower(m), result: strings.ToLower(res), props: map[string]string{}}
		for _, f := range fs[1:] {
			if k, val, ok := strings.Cut(f, "="); ok {
				r.props[strings.ToLower(k)] = strings.ToLower(strings.Trim(val, `"`))
			}
		}
		results = append(results, r)
	}
	return strings.ToLower(fields[0]), results
}

// stripComments drops a header's comments, "(...)", nested or not, and
// keeps quoted strings as they are.
func stripComments(v string) string {
	var b strings.Builder
	depth, quoted := 0, false
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c == '\\' && i+1 < len(v) && (quoted || depth > 0):
			if depth == 0 {
				b.WriteByte(c)
				b.WriteByte(v[i+1])
			}
			i++
		case c == '"' && depth == 0:
			quoted = !quoted
			b.WriteByte(c)
		case c == '(' && !quoted:
			depth++
		case c == ')' && !quoted && depth > 0:
			depth--
		case depth == 0:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// cloudflareResults returns the results of the topmost Authentication-Results
// whose authserv-id is AuthServID.
func cloudflareResults(e rss.Email) ([]authResult, bool) {
	for _, v := range e.AuthenticationResults {
		if id, results := parseResults(v); id == AuthServID {
			return results, true
		}
	}
	return nil, false
}

// resultOf returns the first result for method, "" when there's none.
func resultOf(results []authResult, method string) string {
	for _, r := range results {
		if r.method == method {
			return r.result
		}
	}
	return ""
}

// dmarcPass reports whether DMARC passed for fromDomain.
func dmarcPass(results []authResult, fromDomain string) bool {
	for _, r := range results {
		if r.method == "dmarc" && r.result == "pass" && fromDomain != "" && r.props["header.from"] == fromDomain {
			return true
		}
	}
	return false
}

// dmarcNone reports whether From's domain has no DMARC policy. A policy of
// p=none that fails reads dmarc=fail, not dmarc=none.
func dmarcNone(results []authResult) bool {
	return resultOf(results, "dmarc") == "none"
}

// dkimPassDomains returns the domains whose DKIM signature passed: header.d
// (Cloudflare's), or the domain of header.i (Google's).
func dkimPassDomains(results []authResult) []string {
	var out []string
	for _, r := range results {
		if r.method != "dkim" || r.result != "pass" {
			continue
		}
		d := r.props["header.d"]
		if d == "" {
			d = domain(r.props["header.i"])
		}
		if d != "" {
			out = append(out, d)
		}
	}
	return out
}

// seal is an ARC set's sealer and the results it recorded.
type seal struct {
	sealer  string
	results []authResult
}

// lastSeal returns the highest ARC set not sealed by Cloudflare itself,
// which adds its own set above the others: the last hop before Cloudflare.
// Cloudflare's arc=pass says the chain, this seal included, verified.
func lastSeal(e rss.Email) (seal, bool) {
	for i := len(e.ARC) - 1; i >= 0; i-- {
		id, results := parseResults(e.ARC[i].Results)
		if id == AuthServID {
			continue
		}
		return seal{sealer: e.ARC[i].Sealer, results: results}, true
	}
	return seal{}, false
}

// originalPass reports whether a sealer's recorded results passed the
// original sender: DMARC passing for fromDomain, or no DMARC policy and
// DKIM passing.
func originalPass(results []authResult, fromDomain string) bool {
	switch resultOf(results, "dmarc") {
	case "pass":
		return dmarcPass(results, fromDomain)
	case "none":
		return len(dkimPassDomains(results)) > 0
	}
	return false
}

// authenticatedDomains returns the domains v vouches for: From's domain on
// a DMARC pass, and those whose DKIM signature passed (for a forward, as
// the last seal recorded).
func authenticatedDomains(e rss.Email, v Verdict) []string {
	var results []authResult
	switch v {
	case Pass, PassVia:
		results, _ = cloudflareResults(e)
	case PassForwarded:
		if s, ok := lastSeal(e); ok {
			results = s.results
		}
	default:
		return nil
	}
	out := dkimPassDomains(results)
	if v != PassVia {
		for _, r := range results {
			if r.method == "dmarc" && r.result == "pass" && r.props["header.from"] != "" {
				out = append(out, r.props["header.from"])
			}
		}
	}
	return out
}

// domain returns an address's domain, lowercased, without a trailing dot.
func domain(addr string) string {
	at := strings.LastIndex(addr, "@")
	if at < 0 {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(addr[at+1:]), ".")
}

// orgDomain returns a domain's organizational domain, by the public suffix
// list: news.example.co.uk is example.co.uk.
func orgDomain(d string) string {
	d = strings.TrimSuffix(strings.ToLower(d), ".")
	if o, err := publicsuffix.EffectiveTLDPlusOne(d); err == nil {
		return o
	}
	return d
}
