package admin

import (
	"testing"

	"github.com/grodier/rss/internal/rss"
)

func TestMailLimitsAvailable(t *testing.T) {
	free := MailLimits{Daily: 100, Monthly: 3000, Reserve: 10}
	tests := []struct {
		name   string
		limits MailLimits
		usage  rss.EmailUsage
		want   int
	}{
		{"nothing sent", free, rss.EmailUsage{}, 90},
		{"the day limits", free, rss.EmailUsage{Last24h: 40, ThisMonth: 100}, 50},
		{"the month limits", free, rss.EmailUsage{Last24h: 10, ThisMonth: 2950}, 40},
		{"the day is into its reserve", free, rss.EmailUsage{Last24h: 95, ThisMonth: 100}, 0},
		{"the month is past its limit", free, rss.EmailUsage{Last24h: 0, ThisMonth: 3100}, 0},
		{"no reserve", MailLimits{Daily: 5, Monthly: 50}, rss.EmailUsage{Last24h: 2, ThisMonth: 2}, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.limits.Available(tt.usage); got != tt.want {
				t.Errorf("Available(%+v) = %d, want %d", tt.usage, got, tt.want)
			}
		})
	}
}

func TestSendCheck(t *testing.T) {
	free := MailLimits{Daily: 100, Monthly: 3000, Reserve: 10}
	tests := []struct {
		name               string
		used, n            int
		wantWarn, wantOver bool
		wantFit, wantLeft  int
	}{
		{name: "within 80% of the day", used: 12, n: 5, wantFit: 5, wantLeft: 83},
		{name: "exactly 80%", used: 70, n: 10, wantFit: 10, wantLeft: 20},
		{name: "past 80%", used: 74, n: 8, wantWarn: true, wantFit: 8, wantLeft: 18},
		{name: "exactly what fits", used: 80, n: 10, wantWarn: true, wantFit: 10, wantLeft: 10},
		{name: "more than fits", used: 85, n: 10, wantOver: true, wantFit: 5, wantLeft: 5},
		{name: "none fit", used: 95, n: 3, wantOver: true, wantFit: 0, wantLeft: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newSendCheck(free, rss.EmailUsage{Last24h: tt.used, ThisMonth: tt.used}, tt.n)
			if c.Warn() != tt.wantWarn || c.Over() != tt.wantOver || c.Fit() != tt.wantFit || c.LeftToday != tt.wantLeft {
				t.Errorf("warn, over, fit, left = %t, %t, %d, %d; want %t, %t, %d, %d",
					c.Warn(), c.Over(), c.Fit(), c.LeftToday, tt.wantWarn, tt.wantOver, tt.wantFit, tt.wantLeft)
			}
		})
	}
}

func TestSendCheckMeter(t *testing.T) {
	c := newSendCheck(MailLimits{Daily: 200, Monthly: 3000, Reserve: 20}, rss.EmailUsage{Last24h: 50}, 30)
	if got, want := c.Meter(), (meter{Used: 25, Send: 15, Reserve: 10}); got != want {
		t.Errorf("Meter() = %+v, want %+v", got, want)
	}
	// Over the day, the bar never runs past its end.
	c = newSendCheck(MailLimits{Daily: 100, Monthly: 3000, Reserve: 10}, rss.EmailUsage{Last24h: 120}, 5)
	if got, want := c.Meter(), (meter{Used: 100, Send: 0, Reserve: 10}); got != want {
		t.Errorf("Meter() over the day = %+v, want %+v", got, want)
	}
}
