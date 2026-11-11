package admin

import "github.com/grodier/rss/internal/rss"

// MailLimits are the email provider's sending limits (Resend's free tier by
// default: see "Waitlist and invites" in docs/decisions.md). Reserve emails
// a day and a month are kept for account emails, like password resets,
// which share the limits with invites.
type MailLimits struct {
	Daily, Monthly, Reserve int
}

// warnShare is how much of the day's limit a wave may leave used before the
// review warns.
const warnShare = 0.8

// Available is how many more emails fit under both limits, less the
// reserve, after usage; never negative.
func (l MailLimits) Available(u rss.EmailUsage) int {
	return max(0, min(l.Daily-l.Reserve-u.Last24h, l.Monthly-l.Reserve-u.ThisMonth))
}

// sendCheck is what sending n more emails would do to the limits, as the
// review page shows it.
type sendCheck struct {
	Limits MailLimits
	Usage  rss.EmailUsage
	// N is how many emails would be sent; Available, how many fit.
	N, Available int
	// LeftToday is how many emails the day would have left after sending N.
	LeftToday int
}

func newSendCheck(l MailLimits, u rss.EmailUsage, n int) sendCheck {
	return sendCheck{Limits: l, Usage: u, N: n, Available: l.Available(u), LeftToday: max(0, l.Daily-u.Last24h-n)}
}

// Over reports whether the N emails don't all fit.
func (c sendCheck) Over() bool { return c.N > c.Available }

// Warn reports whether sending N, though they fit, would leave more than
// warnShare of the day's limit used.
func (c sendCheck) Warn() bool {
	return !c.Over() && float64(c.Usage.Last24h+c.N) > warnShare*float64(c.Limits.Daily)
}

// Fit is how many of the N fit: N, or Available when they don't all.
func (c sendCheck) Fit() int { return min(c.N, c.Available) }

// Meter is the day's limit split for the review's bar, in percent of the
// daily limit: what's already used, what this send adds (only what fits),
// and the reserve, which the bar draws at its end, under the other two.
type meter struct{ Used, Send, Reserve float64 }

func (c sendCheck) Meter() meter {
	d := float64(max(1, c.Limits.Daily))
	pct := func(n int) float64 { return min(100, max(0, float64(n)*100/d)) }
	used := pct(c.Usage.Last24h)
	send := min(100-used, pct(c.Fit()))
	return meter{Used: used, Send: send, Reserve: pct(c.Limits.Reserve)}
}
