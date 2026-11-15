package rss

import (
	"regexp"
	"testing"
)

func TestSections(t *testing.T) {
	slugRX := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	names, slugs := map[string]bool{}, map[string]bool{}
	for _, s := range Sections {
		if s.Name == "" {
			t.Errorf("section %q has no name", s.Slug)
		}
		if names[s.Name] {
			t.Errorf("name %q is used twice", s.Name)
		}
		if slugs[s.Slug] {
			t.Errorf("slug %q is used twice", s.Slug)
		}
		names[s.Name], slugs[s.Slug] = true, true
		if !slugRX.MatchString(s.Slug) {
			t.Errorf("slug %q isn't lowercase letters and digits joined by hyphens", s.Slug)
		}
		if got, ok := SectionNamed(s.Name); !ok || got != s {
			t.Errorf("SectionNamed(%q) = %v, %v; want %v, true", s.Name, got, ok, s)
		}
	}
	for _, name := range []string{"", "science", "Sciences", " Science", "film-and-tv"} {
		if got, ok := SectionNamed(name); ok {
			t.Errorf("SectionNamed(%q) = %v, true; want false", name, got)
		}
	}
}
