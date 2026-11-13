package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/grodier/rss/internal/rss"
)

const (
	// PingPeriod is how often the pinger checks www's /healthcheck, and
	// PingTimeout how long www has to answer.
	PingPeriod  = time.Minute
	PingTimeout = 5 * time.Second
	// AvailabilityRetention is how long checks are kept; the pinger deletes
	// older ones every hour.
	AvailabilityRetention = 90 * 24 * time.Hour
	pingTidyPeriod        = time.Hour
	// pingStoreTimeout bounds recording a check and deleting old ones.
	pingStoreTimeout = 10 * time.Second
	// maxHealthcheckBody caps the answer read; www's is under 200 bytes.
	maxHealthcheckBody = 64 << 10
)

// AvailabilityStore stores the pinger's checks; *psql.AvailabilityRepository
// is one.
type AvailabilityStore interface {
	Record(ctx context.Context, c rss.AvailabilityCheck) error
	DeleteBefore(ctx context.Context, before time.Time) error
}

// Pinger checks www's /healthcheck over the private network every minute and
// records each outcome, the inside view of www's availability (see
// "Observability" in docs/decisions.md). URL is a fixed host from the
// configuration, which no user influences, so it doesn't go through
// internal/fetch. The admin runs one instance, so the checks aren't
// coordinated.
type Pinger struct {
	// URL is www's URL on the private network, without a trailing slash.
	URL    string
	Store  AvailabilityStore
	Logger *slog.Logger
	// Timeout is how long www has to answer, PingTimeout when zero, and
	// StoreTimeout how long recording a check or deleting old ones may take,
	// pingStoreTimeout when zero.
	Timeout, StoreTimeout time.Duration

	// down is whether the last recorded check failed, so the log says when
	// www goes down and comes back, not every minute it stays down.
	down bool
}

// Run deletes old checks, then checks www now and every PingPeriod, and
// deletes old checks every hour, until ctx is canceled.
func (p *Pinger) Run(ctx context.Context) {
	p.logger().Info("checking www's availability", "url", p.URL+"/healthcheck", "every", PingPeriod)
	check := time.NewTicker(PingPeriod)
	defer check.Stop()
	tidy := time.NewTicker(pingTidyPeriod)
	defer tidy.Stop()

	p.Tidy(ctx, time.Now())
	p.Ping(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-check.C:
			p.Ping(ctx)
		case now := <-tidy.C:
			p.Tidy(ctx, now)
		}
	}
}

// Ping checks www once and records the outcome. A check cut short by ctx
// is not recorded: it says nothing about www.
func (p *Pinger) Ping(ctx context.Context) {
	c, err := p.Check(ctx)
	if ctx.Err() != nil {
		return
	}
	switch {
	case !c.OK && !p.down:
		p.logger().Warn("www is unavailable", "error", err)
	case c.OK && p.down:
		p.logger().Info("www is available again")
	}
	p.down = !c.OK

	// A write that runs out of pingStoreTimeout is logged; one cut short by
	// shutdown (ctx) isn't.
	sctx, cancel := context.WithTimeout(ctx, p.storeTimeout())
	defer cancel()
	if err := p.Store.Record(sctx, c); err != nil && ctx.Err() == nil {
		p.logger().Error("pinger: record check", "error", err)
	}
}

// Check makes one request to www's /healthcheck and returns its outcome,
// with the reason when www didn't answer "available".
func (p *Pinger) Check(ctx context.Context) (rss.AvailabilityCheck, error) {
	start := time.Now()
	c := rss.AvailabilityCheck{CheckedAt: start, Status: rss.CheckError}

	timeout := p.Timeout
	if timeout == 0 {
		timeout = PingTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	body, err := p.get(ctx)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			c.Status = rss.CheckTimeout
		}
		return c, err
	}

	latency := float64(time.Since(start)) / float64(time.Millisecond)
	c.LatencyMs = &latency
	c.Status = body.Status
	c.Version = body.SystemInfo.Version
	c.OK = body.Status == "available"
	if !c.OK {
		// %.100q: the status is untrusted and up to maxHealthcheckBody long.
		return c, fmt.Errorf("www answered %.100q", c.Status)
	}
	return c, nil
}

// healthcheckBody is the part of www's /healthcheck answer the pinger reads.
type healthcheckBody struct {
	Status     string `json:"status"`
	SystemInfo struct {
		Version string `json:"version"`
	} `json:"system_info"`
}

// get requests /healthcheck and decodes its answer, which must be a 200
// with a status.
func (p *Pinger) get(ctx context.Context) (healthcheckBody, error) {
	var body healthcheckBody
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL+"/healthcheck", nil)
	if err != nil {
		return body, err
	}
	resp, err := pingClient.Do(req)
	if err != nil {
		return body, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return body, fmt.Errorf("www answered HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxHealthcheckBody)).Decode(&body); err != nil {
		return body, fmt.Errorf("read healthcheck: %w", err)
	}
	if body.Status == "" {
		return body, errors.New("healthcheck has no status")
	}
	return body, nil
}

// Tidy deletes the checks older than AvailabilityRetention at now.
func (p *Pinger) Tidy(ctx context.Context, now time.Time) {
	sctx, cancel := context.WithTimeout(ctx, p.storeTimeout())
	defer cancel()
	if err := p.Store.DeleteBefore(sctx, now.Add(-AvailabilityRetention)); err != nil && ctx.Err() == nil {
		p.logger().Error("pinger: delete old checks", "error", err)
	}
}

// pingClient makes every check: no environment proxy (the private
// network is reached directly), a new connection for every check (a kept-alive
// one would stay on one www instance, and on an old one through a deploy), and
// no redirects (www's /healthcheck answers itself, so a redirect is an error).
var pingClient = &http.Client{
	Transport: func() http.RoundTripper {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.Proxy = nil
		t.DisableKeepAlives = true
		return t
	}(),
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func (p *Pinger) storeTimeout() time.Duration {
	if p.StoreTimeout == 0 {
		return pingStoreTimeout
	}
	return p.StoreTimeout
}

func (p *Pinger) logger() *slog.Logger {
	if p.Logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return p.Logger
}
