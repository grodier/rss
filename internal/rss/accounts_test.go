package rss

import (
	"slices"
	"testing"
)

func TestSplitSitesAnswer(t *testing.T) {
	tests := []struct {
		name, answer string
		want         []string
	}{
		{"empty", "", nil},
		{"one", "example.com", []string{"example.com"}},
		{"commas", "a.com,b.com, c.com", []string{"a.com", "b.com", "c.com"}},
		{"semicolons", "a.com; b.com;c.com", []string{"a.com", "b.com", "c.com"}},
		{"new lines", "a.com\nb.com\r\nc.com", []string{"a.com", "b.com", "c.com"}},
		{"mixed", "a.com, Gopher Days;\nthe long feild", []string{"a.com", "Gopher Days", "the long feild"}},
		{"blanks", "a.com,, ;\n\n ,b.com", []string{"a.com", "b.com"}},
		{"a lone separator", ",", nil},
		{"only spaces", "   ", nil},
		{"leading and trailing spaces", "  a.com  ,\t b.com \n", []string{"a.com", "b.com"}},
		{"spaces inside a part kept", "npr. org, Gopher Days", []string{"npr. org", "Gopher Days"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SplitSitesAnswer(tt.answer); !slices.Equal(got, tt.want) {
				t.Errorf("SplitSitesAnswer(%q) = %q, want %q", tt.answer, got, tt.want)
			}
		})
	}
}

func TestFeatures(t *testing.T) {
	var none Features
	if none.Has(FeatureNewsletters) {
		t.Error("the zero set has newsletters")
	}
	if got := NewFeatures(FeatureNewsletters); !got.Has(FeatureNewsletters) {
		t.Errorf("NewFeatures(newsletters) = %b, lacks newsletters", got)
	}
	if NewFeatures(FeatureNewsletters, FeatureNewsletters) != NewFeatures(FeatureNewsletters) {
		t.Error("a repeated feature changes the set")
	}

	unknown := Feature("not-a-feature")
	if got := NewFeatures(unknown); got != none {
		t.Errorf("NewFeatures(unknown) = %b, want the empty set", got)
	}
	if all := ^Features(0); all.Has(unknown) {
		t.Error("a set with every bit has an unknown feature")
	}
}
