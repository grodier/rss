package metrics

import "math"

// Percentile returns the value at quantile q (0 to 1) from histogram
// buckets, interpolating within the bucket; the last (open) bucket returns
// its lower bound. bounds are the buckets' upper bounds, and counts has one
// more element than bounds; the first bucket starts at 0. It returns NaN
// when the counts are empty.
func Percentile(bounds []float64, counts []int, q float64) float64 {
	total := 0
	for _, c := range counts {
		total += c
	}
	if total == 0 {
		return math.NaN()
	}
	rank := min(max(q, 0), 1) * float64(total)
	cum := 0.0
	for i, c := range counts {
		if c <= 0 {
			continue
		}
		if cum+float64(c) >= rank {
			lo := 0.0
			if i > 0 && i-1 < len(bounds) {
				lo = bounds[i-1]
			}
			if i >= len(bounds) {
				return lo
			}
			return lo + (bounds[i]-lo)*(rank-cum)/float64(c)
		}
		cum += float64(c)
	}
	return math.NaN() // unreachable: rank ≤ total
}
