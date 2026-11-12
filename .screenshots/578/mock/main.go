// Mockups for #578's design checkpoint: the web performance page's options,
// drawn with the admin's stylesheet and internal/admin/chart. Throwaway: it
// goes once an option is approved. Run from the repository root:
//
//	go run ./.screenshots/578/mock <out dir>
package main

import (
	"fmt"
	"html/template"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/grodier/rss/internal/admin/chart"
)

var now = time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

var timeBounds = []float64{50, 75, 100, 150, 200, 300, 500, 800, 1000, 1500, 1800, 2500, 3000, 4000, 6000, 10000, 20000}
var clsBounds = []float64{0.01, 0.025, 0.05, 0.1, 0.15, 0.25, 0.4, 0.6, 1}

// metric is one Core Web Vital or loading timing.
type metric struct {
	Key, Name, Full string
	Unit            string // "ms" or ""
	Good, Poor      float64
	Minor           bool
	Counts          []int // histogram, per bound plus one
	Base            float64
}

var metrics = []metric{
	{Key: "lcp", Name: "LCP", Full: "Largest Contentful Paint", Unit: "ms", Good: 2500, Poor: 4000,
		Counts: []int{0, 0, 5, 20, 60, 140, 260, 420, 380, 760, 480, 1130, 380, 360, 230, 110, 40, 10}},
	{Key: "inp", Name: "INP", Full: "Interaction to Next Paint", Unit: "ms", Good: 200, Poor: 500,
		Counts: []int{820, 610, 450, 560, 640, 590, 380, 190, 62, 40, 15, 8, 3, 2, 1, 0, 0, 0}},
	{Key: "cls", Name: "CLS", Full: "Cumulative Layout Shift", Good: 0.1, Poor: 0.25,
		Counts: []int{2600, 610, 690, 520, 170, 110, 60, 22, 8, 2}},
	{Key: "fcp", Name: "FCP", Full: "First Contentful Paint", Unit: "ms", Good: 1800, Poor: 3000, Minor: true,
		Counts: []int{0, 0, 10, 40, 110, 250, 520, 860, 700, 1160, 400, 420, 150, 120, 50, 18, 4, 1}},
	{Key: "ttfb", Name: "TTFB", Full: "Time to First Byte", Unit: "ms", Good: 800, Poor: 1800, Minor: true,
		Counts: []int{40, 120, 300, 640, 760, 900, 820, 380, 250, 270, 90, 110, 60, 40, 20, 8, 2, 1}},
}

func (m metric) bounds() []float64 {
	if m.Unit == "" {
		return clsBounds
	}
	return timeBounds
}

func (m metric) total() int {
	n := 0
	for _, c := range m.Counts {
		n += c
	}
	return n
}

func (m metric) pct(q float64) float64 { return chart.Percentile(m.bounds(), m.Counts, q) }

func (m metric) rating(v float64) string {
	switch {
	case math.IsNaN(v):
		return ""
	case v <= m.Good:
		return "good"
	case v <= m.Poor:
		return "ni"
	}
	return "poor"
}

func (m metric) format(v float64) string {
	if math.IsNaN(v) {
		return "—"
	}
	if m.Unit == "" {
		return fmt.Sprintf("%.2f", v)
	}
	if v >= 1000 {
		return fmt.Sprintf("%.2f s", v/1000)
	}
	return fmt.Sprintf("%.0f ms", v)
}

// shares are the good, needs-improvement and poor percentages.
func (m metric) shares() [3]int {
	var s [3]float64
	b := m.bounds()
	for i, c := range m.Counts {
		hi := math.Inf(1)
		if i < len(b) {
			hi = b[i]
		}
		switch {
		case hi <= m.Good:
			s[0] += float64(c)
		case hi <= m.Poor:
			s[1] += float64(c)
		default:
			s[2] += float64(c)
		}
	}
	t := float64(m.total())
	return [3]int{int(math.Round(s[0] / t * 100)), int(math.Round(s[1] / t * 100)), int(math.Round(s[2] / t * 100))}
}

type pv struct {
	Q      string
	Value  string
	Rating string
	On     bool
}

// Vital is a metric's data on the page.
type Vital struct {
	Name, Full  string
	Minor       bool
	Views       string
	Sel         pv
	All         []pv
	Shares      [3]int
	Chart       template.HTML
	Chart3      template.HTML
	Hist        template.HTML
	Open        bool
	OverTimeSel template.HTML
}

type cell struct {
	Value, Rating, Second, SecondRating string
}

type Row struct {
	Name  string
	Views string
	Cells []cell
	Note  string
	Few   bool
}

type Page struct {
	Option, Period, Pct, DeviceSel string
	Periods, Pcts, Devices         []string
	Empty                          bool
	Views                          string
	Assessment                     string
	AssessmentPass                 bool
	Vitals                         []Vital
	ViewsChart                     template.HTML
	Device, Browser, Installed     []Row
	Routes                         []Row
	MoreRoutes                     int
	Referrers                      [][2]string
	Protocols, Connections         [][2]string
	Long                           bool
}

func series(base, amp, noise float64, n int, step time.Duration, seed float64) []chart.Point {
	ps := make([]chart.Point, n)
	for i := range n {
		t := now.Add(-time.Duration(n-1-i) * step)
		v := base * (1 + amp*math.Sin(float64(i)/3+seed) + noise*math.Sin(float64(i)*2.7+seed*3))
		ps[i] = chart.Point{T: t, V: v}
	}
	return ps
}

var qs = map[string]float64{"p75": 0.75, "p90": 0.90, "p95": 0.95}

func build(opt string, long, empty bool, pct string) Page {
	n, step := 28, 6*time.Hour
	if long {
		n, step = 56, 7*24*time.Hour
	}
	p := Page{Option: opt, Period: "7d", Pct: pct, DeviceSel: "All", Long: long,
		Periods: []string{"24h", "7d", "30d", "90d", "13m"}, Pcts: []string{"p75", "p90", "p95"},
		Devices: []string{"All", "Phone", "Tablet", "Desktop"}, Empty: empty, Views: "4,812"}
	if long {
		p.Period, p.Views = "13m", "61,407"
	}
	if empty {
		return p
	}
	h := 140
	if opt == "r2" {
		h = 170
	}
	markers := []chart.Marker{{T: now.Add(-70 * time.Hour), Label: "a1f3c2e"}, {T: now.Add(-20 * time.Hour), Label: "9bd04aa"}}
	if long {
		markers = nil
	}
	for i, m := range metrics {
		v := Vital{Name: m.Name, Full: m.Full, Minor: m.Minor, Views: count(m.total()), Shares: m.shares()}
		for _, q := range p.Pcts {
			x := m.pct(qs[q])
			e := pv{Q: q, Value: m.format(x), Rating: m.rating(x), On: q == pct}
			v.All = append(v.All, e)
			if e.On {
				v.Sel = e
			}
		}
		lines := map[string][]chart.Point{}
		for _, q := range p.Pcts {
			pts := series(m.pct(qs[q]), 0.08, 0.05, n, step, float64(i)+qs[q]*3)
			if m.Unit == "ms" {
				for j := range pts {
					pts[j].V = math.Round(pts[j].V)
				}
			} else {
				for j := range pts {
					pts[j].V = math.Round(pts[j].V*1000) / 1000
				}
			}
			pts[len(pts)-1].V = m.pct(qs[q]) // the latest is the summary's
			if m.Unit == "ms" {
				pts[len(pts)-1].V = math.Round(pts[len(pts)-1].V)
			} else {
				pts[len(pts)-1].V = math.Round(pts[len(pts)-1].V*100) / 100
			}
			lines[q] = pts
		}
		th := []chart.Threshold{{Value: m.Good, Label: "good"}, {Value: m.Poor, Label: "poor"}}
		v.Chart = chart.LineChart{Title: m.Name + ", " + pct, Unit: m.Unit, Height: h, Markers: markers, Thresholds: th,
			Series: []chart.Series{{Name: pct, Points: lines[pct]}}}.SVG()
		v.Chart3 = chart.LineChart{Title: m.Name + " percentiles", Unit: m.Unit, Height: h, Markers: markers, Thresholds: th,
			Series: []chart.Series{{Name: "p75", Points: lines["p75"]}, {Name: "p90", Points: lines["p90"]}, {Name: "p95", Points: lines["p95"]}}}.SVG()
		v.Hist = hist(m)
		v.Open = i == 0
		p.Vitals = append(p.Vitals, v)
	}
	var failed []string
	for _, v := range p.Vitals[:3] {
		r := v.All[0].Rating
		if r != "good" {
			failed = append(failed, v.Name+" "+map[string]string{"ni": "needs improvement", "poor": "is poor"}[r])
		}
	}
	p.AssessmentPass = len(failed) == 0
	p.Assessment = strings.Join(failed, ", ")

	vc := chart.LineChart{Title: "Page views per day, top routes", Height: 150, Series: []chart.Series{
		{Name: "/", Points: series(380, 0.15, 0.08, 28, 24*time.Hour, 0)},
		{Name: "/articles/{id}", Points: series(210, 0.2, 0.1, 28, 24*time.Hour, 2)},
		{Name: "/feeds", Points: series(70, 0.2, 0.1, 28, 24*time.Hour, 4)}}}
	for i := range vc.Series {
		for j := range vc.Series[i].Points {
			vc.Series[i].Points[j].V = math.Round(vc.Series[i].Points[j].V)
		}
		if !long {
			vc.Series[i].Points = vc.Series[i].Points[21:]
		}
	}
	p.ViewsChart = vc.SVG()

	// Each segment's scale of the overall LCP, INP and CLS, and views.
	seg := func(name string, views int, l, in, c float64, note string) Row {
		r := Row{Name: name, Views: count(views), Note: note, Few: views < 100}
		for k, f := range []float64{l, in, c} {
			m := metrics[k]
			if f == 0 {
				r.Cells = append(r.Cells, cell{Value: "—"})
				continue
			}
			x := m.pct(qs[pct]) * f
			y := m.pct(0.95) * f
			r.Cells = append(r.Cells, cell{Value: m.format(x), Rating: m.rating(x), Second: "p95 " + m.format(y), SecondRating: m.rating(y)})
		}
		return r
	}
	p.Device = []Row{
		seg("Phone", 2904, 1.13, 1.2, 1.2, ""),
		seg("Desktop", 1688, 0.76, 0.5, 0.5, ""),
		seg("Tablet", 220, 0.95, 0.9, 0.8, ""),
	}
	p.Browser = []Row{
		seg("Chrome", 2231, 0.93, 0.8, 0.8, ""),
		seg("Safari", 1906, 1.08, 1.6, 1.2, "Safari’s INP is known to over-report"),
		seg("Firefox", 441, 0.86, 0.7, 0.5, ""),
		seg("Samsung Internet", 143, 2.05, 0, 3, "No INP reported"),
		seg("Other", 43, 0, 0, 1, "No LCP or INP reported"),
	}
	p.Installed = []Row{
		seg("Installed", 1302, 0.8, 0.75, 0.5, ""),
		seg("In the browser", 3510, 1.07, 1.1, 1.2, ""),
	}
	p.Routes = []Row{
		seg("/", 1912, 1.03, 1.05, 1, ""),
		seg("/articles/{id}", 1140, 1.22, 0.7, 2.2, ""),
		seg("/feeds", 498, 0.84, 0.87, 0.3, ""),
		seg("/saved", 402, 0.91, 0.62, 0.7, ""),
		seg("/search", 331, 0.79, 2.15, 0.5, ""),
		seg("/feeds/{id}", 289, 0.96, 0.75, 3, ""),
		seg("/settings", 140, 0.61, 0.37, 0.1, ""),
		seg("/login", 64, 0.52, 0, 0.1, ""),
	}
	p.MoreRoutes = 26
	p.Referrers = [][2]string{{"news.ycombinator.com", "412"}, {"lobste.rs", "128"}, {"www.google.com", "97"}, {"mastodon.social", "61"}, {"duckduckgo.com", "38"}, {"t.co", "22"}, {"feedreader.example", "9"}}
	p.Protocols = [][2]string{{"h2", "62%"}, {"h3", "35%"}, {"http/1.1", "3%"}}
	p.Connections = [][2]string{{"4g", "71%"}, {"3g", "4%"}, {"Not reported", "25%"}}
	return p
}

func count(n int) string {
	s := fmt.Sprint(n)
	if len(s) > 3 {
		s = s[:len(s)-3] + "," + s[len(s)-3:]
	}
	return s
}

// hist draws m's distribution: a bar per bucket, colored by its rating,
// with its percentiles marked. A sketch of what chart.Histogram would gain.
func hist(m metric) template.HTML {
	const h, top, bottom = 130, 16, 108
	b := m.bounds()
	n := len(m.Counts)
	most := 0
	for _, c := range m.Counts {
		most = max(most, c)
	}
	slot := 100 / float64(n)
	pos := func(v float64) float64 { // percent across, interpolated within its bucket
		for i, hi := range b {
			lo := 0.0
			if i > 0 {
				lo = b[i-1]
			}
			if v <= hi {
				return (float64(i) + (v-lo)/(hi-lo)) * slot
			}
		}
		return float64(len(b)) * slot
	}
	var s strings.Builder
	fmt.Fprintf(&s, `<div class="chart chart-histogram dist"><svg class="chart-svg" role="img" height="%d"><title>%s distribution</title>`, h, m.Name)
	s.WriteString(`<g class="chart-grid"><line x1="0" x2="100%" y1="108" y2="108"/></g>`)
	for i, c := range m.Counts {
		if c == 0 {
			continue
		}
		hi := math.Inf(1)
		if i < len(b) {
			hi = b[i]
		}
		r := m.rating(hi)
		if i >= len(b) {
			r = "poor"
		}
		y := bottom - float64(c)/float64(most)*(bottom-top-14)
		fmt.Fprintf(&s, `<rect class="bar-%s" x="%.2f%%" y="%.1f" width="%.2f%%" height="%.1f"/>`, r, float64(i)*slot+slot*0.08, y, slot*0.84, bottom-y)
	}
	for _, q := range []string{"p75", "p90", "p95"} {
		x := pos(m.pct(qs[q]))
		anchor := "middle"
		if x > 92 {
			anchor = "end"
		}
		fmt.Fprintf(&s, `<line class="chart-marker" x1="%.2f%%" x2="%.2f%%" y1="%d" y2="%d"/><text class="chart-marker" x="%.2f%%" y="10" text-anchor="%s">%s</text>`, x, x, top-2, bottom, x, anchor, q)
	}
	s.WriteString(`<g class="chart-labels">`)
	for i := range n {
		if i%2 == 1 && i != n-1 {
			continue
		}
		label := "more"
		if i < len(b) {
			label = m.format(b[i])
			label = strings.TrimSuffix(strings.Replace(label, ".00 s", " s", 1), "")
		}
		fmt.Fprintf(&s, `<text x="%.2f%%" y="%d" text-anchor="middle">%s</text>`, (float64(i)+0.5)*slot, h-6, label)
	}
	s.WriteString(`</g></svg></div>`)
	return template.HTML(s.String())
}

func main() {
	out := os.Args[1]
	dir := filepath.Dir(os.Args[0])
	if len(os.Args) > 2 {
		dir = os.Args[2]
	}
	tpl := template.Must(template.New("").Funcs(template.FuncMap{
		"word": func(r string) string {
			return map[string]string{"good": "Good", "ni": "Needs improvement", "poor": "Poor"}[r]
		},
		"lower": strings.ToLower,
		"dict": func(kv ...any) map[string]any {
			m := map[string]any{}
			for i := 0; i+1 < len(kv); i += 2 {
				m[kv[i].(string)] = kv[i+1]
			}
			return m
		},
		"pctHead": func(p Page) string {
			if p.Option == "r2" {
				return "p75"
			}
			return p.Pct
		},
		"small": func(t, h, v string, rows [][2]string) map[string]any {
			return map[string]any{"T": t, "H": h, "V": v, "Rows": rows}
		},
	}).ParseFiles(filepath.Join(dir, "page.html")))
	for _, c := range []struct {
		name, opt   string
		long, empty bool
		pct         string
	}{
		{"r1-7d", "r1", false, false, "p75"}, {"r1-7d-p95", "r1", false, false, "p95"}, {"r1-13m", "r1", true, false, "p75"}, {"r1-empty", "r1", false, true, "p75"},
		{"r2-7d", "r2", false, false, "p75"}, {"r2-13m", "r2", true, false, "p75"},
		{"r3-7d", "r3", false, false, "p75"}, {"r3-13m", "r3", true, false, "p75"},
	} {
		f, err := os.Create(filepath.Join(out, c.name+".html"))
		if err != nil {
			panic(err)
		}
		if err := tpl.ExecuteTemplate(f, "page.html", build(c.opt, c.long, c.empty, c.pct)); err != nil {
			panic(fmt.Errorf("%s: %w", c.name, err))
		}
		f.Close()
	}
}
