package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// markupOnlyClasses are classes the reader's templates use that have no rule
// on purpose: hooks that scripts or tests select, and names that only
// describe the markup. Anything else with no rule was probably deleted by
// accident (#680 dropped Home's empty-state rules that way, see #753).
var markupOnlyClasses = map[string]bool{
	// Selected by scripts (web/app/features).
	"article-list":   true,
	"search-results": true,
	// Describe the markup; the styles reach them through their parents.
	"check-list":         true,
	"discover-feed-main": true,
	"discover-feeds":     true,
	"recent-search-list": true,
	"save-form":          true,
	"section-head":       true,
	"shared-timeline":    true,
	"site-feeds":         true,
	"site-posts":         true,
	"star-form":          true,
	"timeline-form-lede": true,
	"timeline-list":      true,
	// Parts of inline SVGs and icons, painted by their parents.
	"icon-arrow-down": true,
	"icon-arrow-up":   true,
	"icon-edit":       true,
	"icon-grip":       true,
	"logo-mark":       true,
	"logo-sky":        true,
}

var (
	classAttr  = regexp.MustCompile(`class="([^"]*)"`)
	classToken = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*$`)
)

// Every class the reader's templates write in class="…" has a rule in the
// reader's stylesheets, unless it's on the markup-only list.
func TestTemplateClassesHaveRules(t *testing.T) {
	var css strings.Builder
	for _, name := range stylesheets(t) {
		if strings.HasPrefix(name, "admin") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		css.Write(b)
	}
	sheet := cssComment.ReplaceAllString(css.String(), "")

	used := map[string]bool{}
	root := filepath.Join("..", "internal", "ui", "templates")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".html" {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range classAttr.FindAllStringSubmatch(string(b), -1) {
			for c := range strings.FieldsSeq(m[1]) {
				// Classes built by template actions can't be checked here.
				if classToken.MatchString(c) {
					used[c] = true
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(used) == 0 {
		t.Fatal("found no classes in the templates")
	}

	var orphans []string
	for c := range used {
		if markupOnlyClasses[c] {
			continue
		}
		if !regexp.MustCompile(`\.` + regexp.QuoteMeta(c) + `([^a-zA-Z0-9_-]|$)`).MatchString(sheet) {
			orphans = append(orphans, c)
		}
	}
	slices.Sort(orphans)
	for _, c := range orphans {
		t.Errorf("class %q is used in a template but has no rule in the reader's stylesheets; style it or add it to markupOnlyClasses", c)
	}
	for c := range markupOnlyClasses {
		if !used[c] {
			t.Errorf("markupOnlyClasses lists %q, which no template uses; remove it", c)
		}
	}
}
