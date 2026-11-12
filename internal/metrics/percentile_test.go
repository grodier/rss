package metrics

import (
	"math"
	"testing"
)

func TestPercentile(t *testing.T) {
	bounds := []float64{100, 200, 400}
	even := []int{10, 10, 10, 10}
	nan := math.NaN()
	for _, tt := range []struct {
		name   string
		counts []int
		q      float64
		want   float64
	}{
		{"edge of the first bucket", even, 0.25, 100},
		{"edge of the second bucket", even, 0.5, 200},
		{"inside the first bucket, from 0", even, 0.125, 50},
		{"inside the second bucket", even, 0.375, 150},
		{"inside the third bucket", even, 0.7, 360},
		{"open last bucket", even, 0.9, 400},
		{"the maximum, in the open bucket", even, 1, 400},
		{"empty buckets skipped at the start", []int{0, 10, 0, 0}, 0, 100},
		{"empty buckets skipped at the end", []int{0, 10, 0, 0}, 1, 200},
		{"q below 0 is 0", even, -1, 0},
		{"empty counts", nil, 0.5, nan},
		{"all zero", []int{0, 0, 0, 0}, 0.5, nan},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := Percentile(bounds, tt.counts, tt.q)
			if math.IsNaN(tt.want) {
				if !math.IsNaN(got) {
					t.Errorf("Percentile(%v, %v) = %v, want NaN", tt.counts, tt.q, got)
				}
				return
			}
			if math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("Percentile(%v, %v) = %v, want %v", tt.counts, tt.q, got, tt.want)
			}
		})
	}
}
