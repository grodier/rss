package admin

import (
	"html/template"
	"strings"
	"testing"

	"github.com/grodier/rss/internal/admin/chart"
)

// TestChartFunc checks that the chart template function writes a chart's
// SVG into a page as markup, not as escaped text.
func TestChartFunc(t *testing.T) {
	tmpl := template.Must(template.New("t").Funcs(templateFuncs).Parse(`<section>{{chart .}}</section>`))
	var b strings.Builder
	if err := tmpl.Execute(&b, chart.Histogram{Title: "Latency", Bounds: []float64{10}, Counts: []int{1, 2}}); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); !strings.HasPrefix(got, `<section><div class="chart chart-histogram"><svg class="chart-svg" role="img"`) {
		t.Errorf("chart wrote %s", got)
	}
}
