// Package render reads a service's CPU, memory, instance count and deploys
// from Render's API, for the admin's service page (see "Observability" in
// docs/decisions.md).
//
// Its host is fixed and its service ID comes from the admin's configuration,
// which no user influences, so it uses net/http directly rather than
// internal/fetch. Answers are cached for a minute: the service page asks for
// the same figures on every load.
package render

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// BaseURL is Render's API.
	BaseURL = "https://api.render.com/v1"

	// Timeout bounds each request to Render.
	Timeout = 10 * time.Second

	// CacheFor is how long an answer is reused.
	CacheFor = time.Minute

	// maxBody bounds an answer's size: a 90-day metric at 4-hour steps is
	// a few kilobytes per instance.
	maxBody = 4 << 20
)

// Client reads one service's figures from Render's API.
type Client struct {
	apiKey  string
	service string
	baseURL string
	http    *http.Client
	now     func() time.Time

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	body []byte
	at   time.Time
}

// New returns a client for the service whose ID is service (srv-…), which
// authenticates with apiKey.
func New(apiKey, service string) *Client {
	return &Client{
		apiKey:  apiKey,
		service: service,
		baseURL: BaseURL,
		http:    &http.Client{Timeout: Timeout},
		now:     time.Now,
		cache:   map[string]cached{},
	}
}

// Point is a value at a time.
type Point struct {
	T time.Time
	V float64
}

// Instances are the service's figures over a range. Each is the highest
// instance's at each time, since the limits are per instance and two
// instances overlap during every deploy; the limits are the latest.
type Instances struct {
	CPU         []Point // in CPUs
	Memory      []Point // in bytes
	Count       []Point
	CPULimit    float64 // in CPUs; 0 when Render reported none
	MemoryLimit float64 // in bytes; 0 when Render reported none
}

// Instances returns the service's figures from from to to, a value per step.
func (c *Client) Instances(ctx context.Context, from, to time.Time, step time.Duration) (Instances, error) {
	names := []string{"cpu", "memory", "instance-count", "cpu-limit", "memory-limit"}
	results := make([][]Point, len(names))
	errs := make([]error, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Go(func() { results[i], errs[i] = c.metric(ctx, name, from, to, step) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			return Instances{}, fmt.Errorf("render: %s: %w", names[i], err)
		}
	}
	return Instances{
		CPU:         results[0],
		Memory:      results[1],
		Count:       results[2],
		CPULimit:    latest(results[3]),
		MemoryLimit: latest(results[4]),
	}, nil
}

// timeSeries is Render's answer to a metrics request: a series per
// instance (or per label set), each with its values.
type timeSeries []struct {
	Labels []struct {
		Field string `json:"field"`
		Value string `json:"value"`
	} `json:"labels"`
	Unit   string `json:"unit"`
	Values []struct {
		Timestamp time.Time `json:"timestamp"`
		Value     float64   `json:"value"`
	} `json:"values"`
}

// metric returns the metric's highest value across its series at each
// time, oldest first.
func (c *Client) metric(ctx context.Context, name string, from, to time.Time, step time.Duration) ([]Point, error) {
	q := url.Values{
		"resource":          {c.service},
		"startTime":         {from.UTC().Format(time.RFC3339)},
		"endTime":           {to.UTC().Format(time.RFC3339)},
		"resolutionSeconds": {strconv.Itoa(int(step / time.Second))},
	}
	var series timeSeries
	if err := c.get(ctx, "/metrics/"+name+"?"+q.Encode(), &series); err != nil {
		return nil, err
	}
	highest := map[time.Time]float64{}
	for _, s := range series {
		for _, v := range s.Values {
			t := v.Timestamp.UTC()
			if old, ok := highest[t]; !ok || v.Value > old {
				highest[t] = v.Value
			}
		}
	}
	points := make([]Point, 0, len(highest))
	for t, v := range highest {
		points = append(points, Point{T: t, V: v})
	}
	slices.SortFunc(points, func(a, b Point) int { return a.T.Compare(b.T) })
	return points, nil
}

// latest is the newest point's value, 0 with none.
func latest(points []Point) float64 {
	if len(points) == 0 || math.IsNaN(points[len(points)-1].V) {
		return 0
	}
	return points[len(points)-1].V
}

// Deploy is one of the service's deploys.
type Deploy struct {
	ID      string
	Status  string // "live", "deactivated", "update_failed", …
	Trigger string // "api", "manual", "rollback", …
	// Image is the image's digest (sha256:…) for an image-backed deploy,
	// "" otherwise.
	Image      string
	CreatedAt  time.Time
	FinishedAt time.Time // zero while it runs
}

// WentLive is whether d served traffic: it's live, or was until a later
// deploy replaced it.
func (d Deploy) WentLive() bool {
	return (d.Status == "live" || d.Status == "deactivated") && !d.FinishedAt.IsZero()
}

// deployList is Render's answer to a deploys request.
type deployList []struct {
	Deploy struct {
		ID         string     `json:"id"`
		Status     string     `json:"status"`
		Trigger    string     `json:"trigger"`
		CreatedAt  time.Time  `json:"createdAt"`
		FinishedAt *time.Time `json:"finishedAt"`
		Image      *struct {
			Ref string `json:"ref"`
			Sha string `json:"sha"`
		} `json:"image"`
	} `json:"deploy"`
}

// Deploys returns the service's limit newest deploys (at most 100, Render's
// page size), newest first.
func (c *Client) Deploys(ctx context.Context, limit int) ([]Deploy, error) {
	var list deployList
	if err := c.get(ctx, "/services/"+url.PathEscape(c.service)+"/deploys?limit="+strconv.Itoa(min(limit, 100)), &list); err != nil {
		return nil, fmt.Errorf("render: deploys: %w", err)
	}
	deploys := make([]Deploy, 0, len(list))
	for _, item := range list {
		d := item.Deploy
		deploy := Deploy{ID: d.ID, Status: d.Status, Trigger: d.Trigger, CreatedAt: d.CreatedAt.UTC()}
		if d.FinishedAt != nil {
			deploy.FinishedAt = d.FinishedAt.UTC()
		}
		if d.Image != nil {
			deploy.Image = d.Image.Sha
			if deploy.Image == "" {
				_, deploy.Image, _ = strings.Cut(d.Image.Ref, "@")
			}
		}
		deploys = append(deploys, deploy)
	}
	slices.SortStableFunc(deploys, func(a, b Deploy) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return deploys, nil
}

// get decodes the JSON answer to path, from the cache when it's fresh.
// Errors never include the API key, which goes only in a header.
func (c *Client) get(ctx context.Context, path string, v any) error {
	now := c.now()
	c.mu.Lock()
	hit, ok := c.cache[path]
	c.mu.Unlock()
	if ok && now.Sub(hit.at) < CacheFor {
		return json.Unmarshal(hit.body, v)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return err
	}
	if len(body) > maxBody {
		return fmt.Errorf("answer over %d bytes", maxBody)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for k, e := range c.cache {
		if now.Sub(e.at) >= CacheFor {
			delete(c.cache, k)
		}
	}
	c.cache[path] = cached{body: body, at: now}
	return nil
}
