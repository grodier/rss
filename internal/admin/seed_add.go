package admin

import (
	"cmp"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/grodier/rss/internal/discovery"
	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/seed"
	"github.com/grodier/rss/internal/validator"
)

// Adding sites to the seed list in bulk: the admin pastes a list, one
// address per line with an optional section hint, previews what each line
// will do, then adds them. Nothing is written until Add, which parses and
// checks the text again rather than trusting the preview.

// maxSeedUploadBytes caps /seed/add's posted body, over maxFormBytes, so a
// paste of thousands of lines fits.
const maxSeedUploadBytes = 1 << 20

// maxSeedUploadLines is how many lines one post may have, not counting
// blank and comment lines.
const maxSeedUploadLines = 10_000

// maxSeedSource is the longest source, in characters.
const maxSeedSource = 100

type seedAddForm struct {
	Sites               string `form:"sites"`
	Source              string `form:"source"`
	Step                string `form:"step"` // "preview" or "add"
	validator.Validator `form:"-"`
}

// seedOutcome is a line's outcome as the preview's counts name it.
type seedOutcome struct {
	Outcome, Label string
}

// seedOutcomes are a line's outcomes in the order the preview lists them,
// problems first.
var seedOutcomes = []seedOutcome{
	{seed.LineBadAddress, "Not an address"},
	{seed.LineBadSection, "No such section"},
	{seed.LineAppLink, "On Paper Round"},
	{seed.LineDuplicate, "Repeated"},
	{seed.LineOnList, "Already on the list"},
	{seed.LineAdd, "Will be added"},
}

// seedOutcomeRank is outcome's place in seedOutcomes.
func seedOutcomeRank(outcome string) int {
	return slices.IndexFunc(seedOutcomes, func(o seedOutcome) bool { return o.Outcome == outcome })
}

// seedLineStatus says what will happen to l, in the problems' style; an
// add with a section hint names the section.
func seedLineStatus(l seed.Line, sectionNames map[string]string) feedProblem {
	switch l.Outcome {
	case seed.LineAdd:
		return feedProblem{Text: "Will be added", Detail: sectionNames[l.SectionID]}
	case seed.LineOnList:
		return feedProblem{Text: "Already on the seed list"}
	case seed.LineDuplicate:
		return feedProblem{Text: fmt.Sprintf("Same site as line %d", l.SameAs)}
	case seed.LineBadAddress:
		return feedProblem{Text: "Not a website address", Bad: true}
	case seed.LineBadSection:
		return feedProblem{Text: "No section called “" + l.Section() + "”", Bad: true}
	case seed.LineAppLink:
		return feedProblem{Text: "An address on Paper Round itself", Bad: true}
	}
	return feedProblem{Text: l.Outcome}
}

// seedAddRow is one line of the preview.
type seedAddRow struct {
	seed.Line
	Status feedProblem
}

// seedAddPage is the add form's data, with the preview's once there is one.
type seedAddPage struct {
	Nav  navData
	Form seedAddForm
	// Sections are the sections a line may name.
	Sections []rss.Section
	// Previewed is true once the text has been previewed; Rows are its
	// lines, problems first, and Counts how many have each outcome, those
	// with none left out.
	Previewed bool
	Rows      []seedAddRow
	Counts    []statTile
	// Adds is how many lines Add would add; Add is offered when it's over 0.
	Adds int
}

// seedAddHandler shows the form.
func (s *Server) seedAddHandler(w http.ResponseWriter, r *http.Request) {
	s.renderSeedAdd(w, r, http.StatusOK, seedAddPage{})
}

// renderSeedAdd renders the form with data, filling in its nav and the
// sections.
func (s *Server) renderSeedAdd(w http.ResponseWriter, r *http.Request, status int, data seedAddPage) {
	if data.Sections == nil {
		sections, err := s.services.Feeds.DiscoverSections(r.Context())
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
		data.Sections = sections
	}
	data.Nav = s.nav(r, "seed")
	if err := s.renderHTML(w, status, "seed_add.html", data); err != nil {
		s.serverErrorHTML(w, r, err)
	}
}

// seedAddFormHandler previews or adds the posted list, read under
// maxSeedUploadBytes. Preview shows what each line will do; Add parses and
// checks the text again, adds the lines that can be added, queuing their
// lookups, and goes back to the seed list.
func (s *Server) seedAddFormHandler(w http.ResponseWriter, r *http.Request) {
	var form seedAddForm
	if err := s.decodePostFormLimit(w, r, &form, maxSeedUploadBytes); err != nil {
		s.formErrorResponse(w, r, err)
		return
	}
	form.Source = strings.TrimSpace(form.Source)

	sections, err := s.services.Feeds.DiscoverSections(r.Context())
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	lines := seed.Parse(form.Sites, sections, s.appSiteKey())
	form.CheckField(len(lines) > 0, "sites", "Paste at least one website address.")
	form.CheckField(len(lines) <= maxSeedUploadLines, "sites", "Add at most 10,000 lines at a time.")
	form.CheckField(validator.MaxChars(form.Source, maxSeedSource), "source", "Keep the source under 100 characters.")
	if !form.Valid() {
		s.renderSeedAdd(w, r, http.StatusUnprocessableEntity, seedAddPage{Form: form, Sections: sections})
		return
	}

	var keys []string
	for _, l := range lines {
		if l.Outcome == seed.LineAdd {
			keys = append(keys, l.SiteKey)
		}
	}
	onList, err := s.services.Feeds.SeedOnList(r.Context(), keys)
	if err != nil {
		s.serverErrorHTML(w, r, err)
		return
	}
	var adds []seed.Line
	for i, l := range lines {
		if l.Outcome != seed.LineAdd {
			continue
		}
		if onList[l.SiteKey] {
			lines[i].Outcome = seed.LineOnList
			continue
		}
		adds = append(adds, l)
	}

	if form.Step != "add" {
		s.renderSeedAdd(w, r, http.StatusOK, seedAddPreview(form, lines, sections, len(adds)))
		return
	}

	n := 0
	if len(adds) > 0 {
		n, err = s.services.Feeds.AddSeedSites(r.Context(), adds, form.Source, s.actor(r))
		if err != nil {
			s.serverErrorHTML(w, r, err)
			return
		}
	}
	var msg string
	switch n {
	case 0:
		msg = "Nothing to add: every site is already on the list or couldn't be read."
	case 1:
		msg = "Added 1 site to the seed list; its lookup is queued."
	default:
		msg = "Added " + count(n) + " sites to the seed list; their lookups are queued."
	}
	s.backTo(w, r, "/seed", msg)
}

// seedAddPreview is the add form's data for a preview of lines, adds of
// which can be added.
func seedAddPreview(form seedAddForm, lines []seed.Line, sections []rss.Section, adds int) seedAddPage {
	names := map[string]string{}
	for _, sec := range sections {
		names[sec.ID] = sec.Name
	}
	page := seedAddPage{Form: form, Sections: sections, Previewed: true, Adds: adds}
	counts := map[string]int{}
	for _, l := range lines {
		counts[l.Outcome]++
		page.Rows = append(page.Rows, seedAddRow{Line: l, Status: seedLineStatus(l, names)})
	}
	slices.SortStableFunc(page.Rows, func(a, b seedAddRow) int {
		return cmp.Compare(seedOutcomeRank(a.Outcome), seedOutcomeRank(b.Outcome))
	})
	for _, o := range seedOutcomes {
		if n := counts[o.Outcome]; n > 0 {
			page.Counts = append(page.Counts, statTile{Label: o.Label, Value: n})
		}
	}
	return page
}

// appSiteKey is the reader's own site key, whose addresses the seed list
// refuses; "" when AppURL has no host.
func (s *Server) appSiteKey() string {
	u, err := url.Parse(s.config.AppURL)
	if err != nil || u.Host == "" {
		return ""
	}
	return discovery.SiteKey(u)
}
