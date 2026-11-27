package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/grodier/rss/internal/ratelimit"
	"github.com/grodier/rss/internal/rss"
)

// inboundMaxSkew is how far a signed request's time may be from ours, either
// way; a captured request can be replayed only this long.
const inboundMaxSkew = 5 * time.Minute

// The outcomes of POST /inbound/email, counted as runner inbound's jobs.
const (
	inboundAccepted       = "accepted"
	inboundUnknownAddress = "unknown_address"
	inboundNoAccess       = "no_access"
	inboundTooBig         = "too_big"
	inboundRateLimited    = "rate_limited"
	inboundBadSignature   = "bad_signature"
)

// inboundEmailHandler receives an email for a reader's newsletter address:
// the raw RFC 5322 message as the body, the recipient in X-Inbound-To, and
// X-Inbound-Signature: t=<unix seconds>,v1=<hex HMAC-SHA256 of t + "\n" + to
// + "\n" + body>, keyed with Config.InboundSecret. It stores the message for
// the newsletter runner and answers with a status only (the contract is in
// "Inbound email" in docs/architecture.md): 202 stored, or a replay of an
// email still waiting; 404 no such recipient, or the endpoint off; 413 too
// big; 429 over a limit; 401 a bad or stale signature.
//
// The recipient is a header, not part of the URL, so readers' addresses stay
// out of the request log.
func (s *Server) inboundEmailHandler(w http.ResponseWriter, r *http.Request) {
	if s.config.NewsletterDomain == "" || s.config.InboundSecret == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	start := time.Now()
	respond := func(status int, outcome string) {
		if s.services.JobMetrics != nil {
			s.services.JobMetrics.Observe("inbound", outcome, time.Since(start))
		}
		w.WriteHeader(status)
	}

	// The signature's headers and time are checked before the body is read,
	// so a stale or unsigned request costs nothing.
	to := r.Header.Get("X-Inbound-To")
	ts, mac, ok := parseInboundSignature(r.Header.Get("X-Inbound-Signature"))
	if to == "" || !ok || !inboundFresh(ts, start) {
		respond(http.StatusUnauthorized, inboundBadSignature)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, rss.InboundMaxBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			respond(http.StatusRequestEntityTooLarge, inboundTooBig)
			return
		}
		// The sender went away or sent a broken body: nothing to count.
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !hmac.Equal(mac, inboundMAC(s.config.InboundSecret, ts, to, raw)) {
		respond(http.StatusUnauthorized, inboundBadSignature)
		return
	}

	localPart, ok := inboundLocalPart(to, s.config.NewsletterDomain)
	if !ok {
		respond(http.StatusNotFound, inboundUnknownAddress)
		return
	}
	addressID, userID, err := s.services.InboundService.ResolveAddress(r.Context(), localPart)
	switch {
	case errors.Is(err, rss.ErrNoRecord):
		respond(http.StatusNotFound, inboundUnknownAddress)
		return
	case errors.Is(err, rss.ErrNoAccess):
		respond(http.StatusNotFound, inboundNoAccess)
		return
	case err != nil:
		s.logError(r, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if ok, retry := s.inboundAddressLimiter.AllowWithRetry(addressID); !ok {
		ratelimit.SetRetryAfter(w, retry)
		respond(http.StatusTooManyRequests, inboundRateLimited)
		return
	}
	if ok, retry := s.inboundReaderLimiter.AllowWithRetry(userID); !ok {
		ratelimit.SetRetryAfter(w, retry)
		respond(http.StatusTooManyRequests, inboundRateLimited)
		return
	}

	err = s.services.InboundService.Store(r.Context(), addressID, userID, raw)
	switch {
	case err == nil, errors.Is(err, rss.ErrDuplicateInbound):
		// A replay of an email still waiting is accepted again: the first
		// copy will become the article.
		respond(http.StatusAccepted, inboundAccepted)
	case errors.Is(err, rss.ErrInboundFull):
		respond(http.StatusTooManyRequests, inboundRateLimited)
	case errors.Is(err, rss.ErrNoRecord):
		// The reader deleted their account since ResolveAddress.
		respond(http.StatusNotFound, inboundUnknownAddress)
	default:
		s.logError(r, err)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

// parseInboundSignature reads X-Inbound-Signature, "t=<unix seconds>,v1=<hex
// MAC>", returning t as sent (it's signed as text) and the MAC's bytes.
func parseInboundSignature(h string) (ts string, mac []byte, ok bool) {
	for part := range strings.SplitSeq(h, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch k {
		case "t":
			ts = v
		case "v1":
			b, err := hex.DecodeString(v)
			if err != nil {
				return "", nil, false
			}
			mac = b
		}
	}
	return ts, mac, ts != "" && len(mac) == sha256.Size
}

// inboundFresh reports whether ts, unix seconds, is within inboundMaxSkew of
// now.
func inboundFresh(ts string, now time.Time) bool {
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	d := now.Sub(time.Unix(sec, 0))
	return d <= inboundMaxSkew && d >= -inboundMaxSkew
}

// inboundMAC is the HMAC-SHA256, keyed with secret, of ts + "\n" + to + "\n"
// + body: what X-Inbound-Signature's v1 carries.
func inboundMAC(secret, ts, to string, body []byte) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts + "\n" + to + "\n"))
	m.Write(body)
	return m.Sum(nil)
}

// inboundLocalPart returns the local part of to, an address at domain (the
// -newsletter-domain), lowercased and without a "+tag", so
// "George.K7X2M9+news@In.Example.com" is "george.k7x2m9". It reports false
// for an address at any other domain, or without a local part.
func inboundLocalPart(to, domain string) (string, bool) {
	local, ok := strings.CutSuffix(strings.ToLower(strings.TrimSpace(to)), "@"+domain)
	if !ok {
		return "", false
	}
	local, _, _ = strings.Cut(local, "+")
	if local == "" || strings.Contains(local, "@") {
		return "", false
	}
	return local, true
}
