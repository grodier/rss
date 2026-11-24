package psql

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/grodier/rss/internal/metrics"
	"github.com/grodier/rss/internal/rss"
)

// endpointsRow is requestRow with a method and status class.
func endpointsRow(period time.Time, instance, route, method string, class, requests int, buckets ...int64) metrics.RequestRow {
	r := requestRow(period, instance, route, requests, buckets...)
	r.Method = method
	r.StatusClass = class
	return r
}

// ints returns n as a bucket array, padded to every bucket.
func ints(n ...int) []int {
	b := make([]int, len(metrics.DurationBounds)+1)
	copy(b, n)
	return b
}

// TestAdminEndpointsMinute reads minute rows: totals, steps, routes and
// instances, with each group's buckets summed element-wise.
func TestAdminEndpointsMinute(t *testing.T) {
	t.Parallel()
	metricsRepo, db := newMetricsRepo(t)
	repo := NewAdminEndpointsRepository(db)
	ctx := context.Background()
	from := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

	err := metricsRepo.SaveRequests(ctx, []metrics.RequestRow{
		endpointsRow(from.Add(1*time.Minute), "web-1", "/feeds/{id}", "GET", 2, 3, 1, 2),
		endpointsRow(from.Add(2*time.Minute), "web-2", "/feeds/{id}", "GET", 2, 2, 0, 1, 1),
		endpointsRow(from.Add(2*time.Minute), "web-2", "/feeds/{id}", "GET", 5, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1),
		endpointsRow(from.Add(12*time.Minute), "web-1", "/feeds/{id}", "GET", 4, 4, 4),
		endpointsRow(from.Add(12*time.Minute), "web-1", "/feeds/{id}", "POST", 3, 1, 1),
		endpointsRow(from.Add(13*time.Minute), "web-1", "/", "GET", 2, 5, 5),
		// Outside the window.
		endpointsRow(from.Add(-time.Minute), "web-1", "/", "GET", 2, 7, 7),
		endpointsRow(from.Add(20*time.Minute), "web-1", "/", "GET", 2, 9, 9),
	})
	if err != nil {
		t.Fatalf("SaveRequests: %v", err)
	}

	q := rss.EndpointQuery{From: from, To: from.Add(20 * time.Minute), Step: 10 * time.Minute}
	got, err := repo.Endpoints(ctx, q)
	if err != nil {
		t.Fatalf("Endpoints: %v", err)
	}
	if got.Total.Requests != 16 || got.Total.Classes != [5]int{0, 10, 1, 4, 1} {
		t.Errorf("total = %d requests, classes %v; want 16, [0 10 1 4 1]", got.Total.Requests, got.Total.Classes)
	}
	if got.Total.BytesSum != 1600 || got.Total.DurationMsSum != 160 {
		t.Errorf("total sums = %d bytes, %v ms; want 1600, 160", got.Total.BytesSum, got.Total.DurationMsSum)
	}
	if want := ints(11, 3, 1, 0, 0, 0, 0, 0, 0, 0, 0, 1); !slices.Equal(got.Total.Buckets, want) {
		t.Errorf("total buckets = %v, want %v", got.Total.Buckets, want)
	}

	if len(got.Series) != 2 {
		t.Fatalf("series = %+v, want 2 steps", got.Series)
	}
	if !got.Series[0].T.Equal(from) || got.Series[0].Requests != 6 || got.Series[0].Classes[4] != 1 {
		t.Errorf("first step = %+v, want 6 requests at %v, one 5xx", got.Series[0], from)
	}
	if want := ints(1, 3, 1, 0, 0, 0, 0, 0, 0, 0, 0, 1); !slices.Equal(got.Series[0].Buckets, want) {
		t.Errorf("first step's buckets = %v, want %v", got.Series[0].Buckets, want)
	}
	if !got.Series[1].T.Equal(from.Add(10*time.Minute)) || got.Series[1].Requests != 10 {
		t.Errorf("second step = %+v, want 10 requests", got.Series[1])
	}

	var routes []string
	for _, r := range got.Routes {
		routes = append(routes, fmt.Sprintf("%s %s %d", r.Method, r.Route, r.Requests))
	}
	if want := []string{"GET / 5", "GET /feeds/{id} 10", "POST /feeds/{id} 1"}; !slices.Equal(routes, want) {
		t.Errorf("routes = %v, want %v", routes, want)
	}
	if want := ints(5, 3, 1, 0, 0, 0, 0, 0, 0, 0, 0, 1); !slices.Equal(got.Routes[1].Buckets, want) {
		t.Errorf("GET /feeds/{id}'s buckets = %v, want %v", got.Routes[1].Buckets, want)
	}
	if got.Instances != nil {
		t.Errorf("instances = %+v without a route, want none", got.Instances)
	}

	// One route: its instances, and no routes.
	q.Route, q.Method = "/feeds/{id}", "GET"
	got, err = repo.Endpoints(ctx, q)
	if err != nil {
		t.Fatalf("Endpoints for a route: %v", err)
	}
	if got.Total.Requests != 10 || got.Routes != nil {
		t.Errorf("route's total = %d, routes %+v; want 10, none", got.Total.Requests, got.Routes)
	}
	var instances []string
	for _, in := range got.Instances {
		instances = append(instances, fmt.Sprintf("%s %d %d", in.Instance, in.Requests, in.Classes[4]))
	}
	if want := []string{"web-1 7 0", "web-2 3 1"}; !slices.Equal(instances, want) {
		t.Errorf("instances = %v, want %v", instances, want)
	}

	// A route with no requests reads as nothing, not an error.
	q.Route = "/nowhere"
	got, err = repo.Endpoints(ctx, q)
	if err != nil {
		t.Fatalf("Endpoints for an unknown route: %v", err)
	}
	if got.Total.Requests != 0 || got.Series != nil || len(got.Total.Buckets) != len(metrics.DurationBounds)+1 {
		t.Errorf("unknown route = %+v, want no requests and empty buckets", got)
	}
}

// TestAdminEndpointsHourly reads the hourly rows of the hours rolled up,
// and the minute rows of the hours after them, once each.
func TestAdminEndpointsHourly(t *testing.T) {
	t.Parallel()
	metricsRepo, db := newMetricsRepo(t)
	repo := NewAdminEndpointsRepository(db)
	ctx := context.Background()
	hour := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

	err := metricsRepo.SaveRequests(ctx, []metrics.RequestRow{
		endpointsRow(hour.Add(1*time.Minute), "web-1", "/", "GET", 2, 2, 2),
		endpointsRow(hour.Add(30*time.Minute), "web-2", "/", "GET", 5, 3, 0, 3),
		// Not rolled up yet.
		endpointsRow(hour.Add(65*time.Minute), "web-1", "/", "GET", 2, 4, 0, 0, 4),
		endpointsRow(hour.Add(70*time.Minute), "web-2", "/", "GET", 2, 1, 1),
	})
	if err != nil {
		t.Fatalf("SaveRequests: %v", err)
	}
	if err := metricsRepo.RollUp(ctx, hour.Add(90*time.Minute)); err != nil {
		t.Fatalf("RollUp: %v", err)
	}

	q := rss.EndpointQuery{From: hour.Add(-time.Hour), To: hour.Add(3 * time.Hour), Hourly: true, Step: time.Hour, Route: "/", Method: "GET"}
	got, err := repo.Endpoints(ctx, q)
	if err != nil {
		t.Fatalf("Endpoints: %v", err)
	}
	if got.Total.Requests != 10 || got.Total.Classes[4] != 3 {
		t.Errorf("total = %d requests, %d 5xx; want 10, 3", got.Total.Requests, got.Total.Classes[4])
	}
	if want := ints(3, 3, 4); !slices.Equal(got.Total.Buckets, want) {
		t.Errorf("total buckets = %v, want %v", got.Total.Buckets, want)
	}
	var steps []string
	for _, p := range got.Series {
		steps = append(steps, fmt.Sprintf("%s %d", p.T.Format("15:04"), p.Requests))
	}
	if want := []string{"10:00 5", "11:00 5"}; !slices.Equal(steps, want) {
		t.Errorf("steps = %v, want %v", steps, want)
	}
	if got.Instances != nil {
		t.Errorf("instances = %+v from hourly rows, want none", got.Instances)
	}

	// Minute rows of the same window count the same requests.
	q.Hourly = false
	minutes, err := repo.Endpoints(ctx, q)
	if err != nil {
		t.Fatalf("Endpoints from minutes: %v", err)
	}
	if minutes.Total.Requests != got.Total.Requests || !slices.Equal(minutes.Total.Buckets, got.Total.Buckets) {
		t.Errorf("minute total = %+v, hourly %+v; want the same", minutes.Total, got.Total)
	}
}

func TestAdminEndpointsRejectsNoStep(t *testing.T) {
	t.Parallel()
	_, db := newMetricsRepo(t)
	if _, err := NewAdminEndpointsRepository(db).Endpoints(context.Background(), rss.EndpointQuery{}); err == nil {
		t.Error("Endpoints with no step: want an error")
	}
}

func TestAdminEndpointsRouteErrors(t *testing.T) {
	t.Parallel()
	_, db := newMetricsRepo(t)
	errs := NewServerErrorRepository(db)
	repo := NewAdminEndpointsRepository(db)
	ctx := context.Background()
	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

	for i, e := range []rss.ServerError{
		{OccurredAt: at, Instance: "web-1", Route: "/feeds/{id}", Method: "GET", Message: "first"},
		{OccurredAt: at.Add(time.Minute), Instance: "web-2", Route: "/feeds/{id}", Method: "GET", Message: "second", Panic: true},
		{OccurredAt: at.Add(2 * time.Minute), Instance: "web-1", Route: "/feeds/{id}", Method: "GET", Message: "third"},
		{OccurredAt: at.Add(3 * time.Minute), Instance: "web-1", Route: "/feeds/{id}", Method: "POST", Message: "another method"},
		{OccurredAt: at.Add(4 * time.Minute), Instance: "web-1", Route: "/", Method: "GET", Message: "another route"},
	} {
		if err := errs.Record(ctx, e); err != nil {
			t.Fatalf("Record %d: %v", i, err)
		}
	}

	got, err := repo.RouteErrors(ctx, "/feeds/{id}", "GET", 2)
	if err != nil {
		t.Fatalf("RouteErrors: %v", err)
	}
	if len(got) != 2 || got[0].Message != "third" || got[1].Message != "second" {
		t.Fatalf("errors = %+v, want third, then second", got)
	}
	if e := got[1]; !e.Panic || e.Instance != "web-2" || !e.OccurredAt.Equal(at.Add(time.Minute)) {
		t.Errorf("second = %+v, want web-2's panic at %v", e, at.Add(time.Minute))
	}

	got, err = repo.RouteErrors(ctx, "/nowhere", "GET", 50)
	if err != nil || len(got) != 0 {
		t.Errorf("RouteErrors for an unknown route = %+v, %v; want none", got, err)
	}
}
