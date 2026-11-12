package metrics

// rumTimeBounds are the upper bounds, in milliseconds, of the real-user
// timings' buckets: roughly log-spaced from 50 ms to 20 s, with each Core Web
// Vital's "good" and "poor" thresholds among them (INP's 200 and 500, TTFB's
// 800 and 1800, FCP's 1800 and 3000, LCP's 2500 and 4000), so the share of
// good and poor views is exact.
var rumTimeBounds = []float64{50, 75, 100, 150, 200, 300, 500, 800, 1000, 1500, 1800, 2500, 3000, 4000, 6000, 10000, 20000}

// rumCLSBounds are CLS's, from 0.01 to 1, with its thresholds, 0.1 and 0.25,
// among them.
var rumCLSBounds = []float64{0.01, 0.025, 0.05, 0.1, 0.15, 0.25, 0.4, 0.6, 1}

// RUMBounds are the upper bounds of each real-user metric's histogram
// buckets in rum_daily, by its metric; a last bucket counts everything above
// the last bound, and a value on a bound is in that bound's bucket, as in
// Bucket. The server's time is mostly under 50 ms, so it has the request
// metrics' DurationBounds.
var RUMBounds = map[string][]float64{
	"ttfb":   rumTimeBounds,
	"fcp":    rumTimeBounds,
	"lcp":    rumTimeBounds,
	"inp":    rumTimeBounds,
	"cls":    rumCLSBounds,
	"server": DurationBounds,
}

// RUMThresholds are Google's limits for each real-user metric the admin
// rates: a value at or under Good is good, one above Poor is poor, and one
// between needs improvement. Each is among the metric's RUMBounds, so the
// shares counted from rum_daily's buckets are exact.
var RUMThresholds = map[string]Thresholds{
	"ttfb": {Good: 800, Poor: 1800},
	"fcp":  {Good: 1800, Poor: 3000},
	"lcp":  {Good: 2500, Poor: 4000},
	"inp":  {Good: 200, Poor: 500},
	"cls":  {Good: 0.1, Poor: 0.25},
}

// Thresholds are a metric's "good" and "poor" limits.
type Thresholds struct {
	Good, Poor float64
}
