package admin

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/grodier/rss/internal/rss"
	"github.com/grodier/rss/internal/seed"
)

// testUpload is a paste with a line of each outcome: two adds (one with a
// section hint), one already on the list (found.example, in seedKeys), a
// repeat, a bad address with markup, an unknown section, and the reader's
// own site (testAppURL's).
const testUpload = `# A comment, skipped
new.example, Food
https://feeds.other.example/rss.xml

found.example
www.new.example
<b>not a site</b>
gardens.example, Gardening
https://app.example.com/discover
`

func TestSeedAddPage(t *testing.T) {
	b := seedServer(t, &fakeFeeds{})
	rr := b.get(t, "/seed/add")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /seed/add = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<a href="/seed" aria-current="page">Seed list</a>`,
		`<form class="seed-add" action="/seed/add" method="post" data-busy>`,
		`<textarea id="sites" name="sites"`,
		`<input type="text" id="source" name="source" value=""`,
		// The sections a line may name.
		`(Science, Food, Film and TV)`,
		`name="step" value="preview"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("add page lacks %s", want)
		}
	}
	if strings.Contains(body, `value="add"`) || strings.Contains(body, "preview-heading") {
		t.Error("the add page offers Add or a preview before Preview")
	}

	// The seed list links to it.
	b = seedServer(t, &fakeFeeds{seedCounts: testSeedCounts})
	if body := b.get(t, "/seed").Body.String(); !strings.Contains(body, `<a class="btn btn-sm btn-primary" href="/seed/add">Add sites</a>`) {
		t.Error("the seed list doesn't link to Add sites")
	}
}

func TestSeedAddPreview(t *testing.T) {
	feeds := &fakeFeeds{seedKeys: []string{"found.example"}}
	b := seedServer(t, feeds)

	rr := b.post(t, "/seed/add", url.Values{"sites": {testUpload}, "source": {" IndieWeb list "}, "step": {"preview"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("Preview = %d, want 200:\n%s", rr.Code, rr.Body)
	}
	body := rr.Body.String()
	// Each outcome with its string, problems first, then in line order.
	order := []string{
		`<td class="num">7</td>`, `<strong>Not a website address</strong>`,
		`<td class="num">8</td>`, `<strong>No section called “Gardening”</strong>`,
		`<td class="num">9</td>`, `<strong>An address on Paper Round itself</strong>`,
		`<td class="num">6</td>`, `<strong>Same site as line 2</strong>`,
		`<td class="num">5</td>`, `<strong>Already on the seed list</strong>`,
		`<td class="num">2</td>`, `<strong>Will be added</strong><span class="problem-detail"> · Food</span>`,
		`<td class="num">3</td>`, `<strong>Will be added</strong></span>`,
	}
	at := 0
	for _, want := range order {
		i := strings.Index(body[at:], want)
		if i < 0 {
			t.Fatalf("preview lacks %s after byte %d, or has it out of order", want, at)
		}
		at += i + len(want)
	}
	for _, want := range []string{
		// The counts per outcome.
		`<dt>Not an address</dt>`, `<dt>Will be added</dt>`,
		// The text and source are kept.
		// After a newline, which the browser drops, so a paste's own
		// leading newline survives a preview and its line numbers stay.
		`>` + "\n" + html.EscapeString("# A comment, skipped\nnew.example, Food"),
		`value="IndieWeb list"`,
		// Add, with its count.
		`<button class="btn btn-primary" type="submit" name="step" value="add" data-busy-label="Adding…"><span data-busy-text>Add 2 sites</span></button>`,
		// Markup in a line is escaped.
		`&lt;b&gt;not a site&lt;/b&gt;`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("preview lacks %s", want)
		}
	}
	if strings.Contains(body, "<b>not a site</b>") {
		t.Error("preview renders a line as markup")
	}
	if len(feeds.calls) != 0 {
		t.Errorf("Preview wrote %+v", feeds.calls)
	}

	// One site to add says so; none offers no Add.
	body = b.post(t, "/seed/add", url.Values{"sites": {"one.example"}, "step": {"preview"}}).Body.String()
	if !strings.Contains(body, "<span data-busy-text>Add 1 site</span>") {
		t.Error("preview of one site doesn't offer Add 1 site")
	}
	body = b.post(t, "/seed/add", url.Values{"sites": {"found.example\nnope"}, "step": {"preview"}}).Body.String()
	if strings.Contains(body, `value="add"`) || !strings.Contains(body, "Nothing here can be added.") {
		t.Error("preview with nothing to add offers Add")
	}
}

func TestSeedAdd(t *testing.T) {
	feeds := &fakeFeeds{seedKeys: []string{"found.example"}, seedCounts: testSeedCounts}
	b := seedServer(t, feeds)

	rr := b.post(t, "/seed/add", url.Values{"sites": {testUpload}, "source": {" waitlist "}, "step": {"add"}})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/seed" {
		t.Fatalf("Add = %d to %q, want 303 to /seed", rr.Code, rr.Header().Get("Location"))
	}
	if len(feeds.calls) != 1 || feeds.calls[0].Action != "seed-add" || feeds.calls[0].ID != "waitlist" || feeds.calls[0].By.AdminEmail != testEmail {
		t.Errorf("calls = %+v, want one seed-add from waitlist by %s", feeds.calls, testEmail)
	}
	var got []string
	for _, l := range feeds.seedAdded {
		if l.Outcome != seed.LineAdd {
			t.Errorf("added line %d with outcome %s", l.N, l.Outcome)
		}
		got = append(got, fmt.Sprintf("%d %s %s", l.N, l.SiteKey, l.SectionID))
	}
	if want := []string{"2 new.example " + testSections[1].ID, "3 feeds.other.example "}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("added %q, want %q", got, want)
	}
	if body := html.UnescapeString(b.get(t, "/seed").Body.String()); !strings.Contains(body, "Added 2 sites to the seed list; their lookups are queued.") {
		t.Error("seed list doesn't flash the sites added")
	}

	b.post(t, "/seed/add", url.Values{"sites": {"one.example"}, "step": {"add"}})
	if body := html.UnescapeString(b.get(t, "/seed").Body.String()); !strings.Contains(body, "Added 1 site to the seed list; its lookup is queued.") {
		t.Error("seed list doesn't flash the one site added")
	}

	// Nothing to add doesn't call the store.
	feeds.calls = nil
	b.post(t, "/seed/add", url.Values{"sites": {"found.example\nnope"}, "step": {"add"}})
	if len(feeds.calls) != 0 {
		t.Errorf("Add with nothing to add called %+v", feeds.calls)
	}
	if body := html.UnescapeString(b.get(t, "/seed").Body.String()); !strings.Contains(body, "Nothing to add: every site is already on the list or couldn't be read.") {
		t.Error("seed list doesn't flash that there was nothing to add")
	}

	feeds.err = errors.New("db down")
	if rr := b.post(t, "/seed/add", url.Values{"sites": {"two.example"}, "step": {"add"}}); rr.Code != http.StatusInternalServerError {
		t.Errorf("Add failing = %d, want 500", rr.Code)
	}
}

func TestSeedAddInvalid(t *testing.T) {
	tooMany := strings.Repeat("a.example\n", maxSeedUploadLines+1)
	tests := []struct {
		name          string
		form          url.Values
		field, want   string
		keepSites     string
		keepSourceVal string
	}{
		{"no sites", url.Values{"sites": {" \n"}, "source": {"list"}}, "sites", "Paste at least one website address.", "", "list"},
		{"only comments", url.Values{"sites": {"# none\n"}}, "sites", "Paste at least one website address.", "# none", ""},
		{"too many lines", url.Values{"sites": {tooMany}}, "sites", "Add at most 10,000 lines at a time.", "a.example\na.example", ""},
		{"source too long", url.Values{"sites": {"<i>a.example</i>"}, "source": {strings.Repeat("é", 101)}}, "source", "Keep the source under 100 characters.", "&lt;i&gt;a.example&lt;/i&gt;", strings.Repeat("é", 101)},
	}
	for _, tt := range tests {
		for _, step := range []string{"preview", "add"} {
			feeds := &fakeFeeds{}
			b := seedServer(t, feeds)
			tt.form.Set("step", step)
			rr := b.post(t, "/seed/add", tt.form)
			if rr.Code != http.StatusUnprocessableEntity {
				t.Errorf("%s (%s) = %d, want 422", tt.name, step, rr.Code)
				continue
			}
			body := rr.Body.String()
			for _, want := range []string{
				`<p class="error" id="` + tt.field + `-error">` + tt.want + `</p>`,
				`aria-invalid="true"`,
				tt.keepSites,
				`value="` + tt.keepSourceVal + `"`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("%s (%s) lacks %s", tt.name, step, want)
				}
			}
			if strings.Contains(body, "preview-heading") || len(feeds.calls) != 0 {
				t.Errorf("%s (%s) previewed or wrote %+v", tt.name, step, feeds.calls)
			}
		}
	}
}

// TestSeedAddLimit checks /seed/add reads a paste over the other forms'
// limit, and refuses one over its own.
func TestSeedAddLimit(t *testing.T) {
	b := seedServer(t, &fakeFeeds{})
	big := strings.Repeat("# "+strings.Repeat("x", 98)+"\n", (maxFormBytes/100)+10) + "a.example"
	if rr := b.post(t, "/seed/add", url.Values{"sites": {big}, "step": {"preview"}}); rr.Code != http.StatusOK {
		t.Errorf("Preview of %d bytes = %d, want 200", len(big), rr.Code)
	}
	huge := strings.Repeat("x", maxSeedUploadBytes)
	if rr := b.post(t, "/seed/add", url.Values{"sites": {huge}, "step": {"preview"}}); rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("Preview over 1 MiB = %d, want 413", rr.Code)
	}
}

// TestSeedAddFails checks the form answers 500 when its store fails.
func TestSeedAddFails(t *testing.T) {
	b := seedServer(t, &fakeFeeds{err: errors.New("db down")})
	if rr := b.post(t, "/seed/add", url.Values{"sites": {"a.example"}, "step": {"preview"}}); rr.Code != http.StatusInternalServerError {
		t.Errorf("Preview failing = %d, want 500", rr.Code)
	}
}

func TestSeedPageHintAndSource(t *testing.T) {
	entries := testSeedEntries()[:3]
	entries[0].SectionName, entries[0].Source = "Food", "waitlist"
	entries[1].SectionName = "<b>Film</b>"
	entries[2].Source = "IndieWeb list"
	b := seedServer(t, &fakeFeeds{seed: entries, seedCounts: testSeedCounts})
	body := b.get(t, "/seed").Body.String()
	for _, want := range []string{
		`found.example<small>For Food · From waitlist</small>`,
		`single.example<small>For &lt;b&gt;Film&lt;/b&gt;</small>`,
		`failed.example<small>From IndieWeb list</small>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("seed page lacks %s", want)
		}
	}
}

// seedWaitlistServer returns a logged-in browser to a server with the
// waitlist wl and the seed list's fake feeds.
func seedWaitlistServer(t *testing.T, wl *fakeWaitlist) *browser {
	t.Helper()
	cfg := Config{Port: 4001, Env: "development", Version: "test", BaseURL: "http://localhost:4001", AppURL: testAppURL}
	s, _ := newTestServerWithAdminConfig(t, cfg, Services{Database: pinger{}, Feeds: &fakeFeeds{}, Waitlist: wl, Audit: &fakeAudit{}})
	return loggedIn(t, s)
}

func TestSeedAddWaitlistButton(t *testing.T) {
	b := waitlistServer(t, testWaitlist(), &fakeInvites{}, &fakeEmails{})
	body := b.get(t, "/waitlist").Body.String()
	// In the selection form, so it posts the ticked entries' id fields.
	form := body[strings.Index(body, `<form id="selection"`):]
	form = form[:strings.Index(form, "</form>")]
	if !strings.Contains(form, `name="id" value="`+entryID(1)+`"`) ||
		!strings.Contains(form, `<button class="btn btn-sm" type="submit" formaction="/seed/add/waitlist">Add their sites to the seed list</button>`) {
		t.Errorf("the waitlist's selection form lacks the button or the IDs:\n%s", form)
	}
	if body := b.get(t, "/waitlist?tab=invited").Body.String(); strings.Contains(body, "/seed/add/waitlist") {
		t.Error("the Invited tab offers adding sites; only the Waiting tab selects people")
	}
}

func TestSeedAddWaitlist(t *testing.T) {
	first, second, third, unselected := waitingRow(1), waitingRow(2), waitingRow(3), waitingRow(4)
	first.Sites = "lwn.net, Gopher Days; https://www.example.com/blog\nthe long feild"
	second.Sites = "LWN.net;example.com, <b>Gopher</b> & co, gopher days,npr. org"
	third.Sites = ""
	unselected.Sites = "unselected.example"
	b := seedWaitlistServer(t, &fakeWaitlist{rows: []rss.WaitlistRow{first, second, third, unselected}})

	rr := b.post(t, "/seed/add/waitlist", url.Values{"id": {entryID(1), entryID(2), entryID(3)}})
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /seed/add/waitlist = %d, want 200:\n%s", rr.Code, rr.Body)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<form class="seed-add" action="/seed/add" method="post" data-busy>`,
		// Two of the three answered.
		"Sites from 2 waitlist entries.",
		// Each site once, with its first spelling, one per line; LWN.net
		// and example.com are the same sites as earlier lines.
		">\nlwn.net\nhttps://www.example.com/blog\nnpr.org</textarea>",
		`<input type="text" id="source" name="source" value="waitlist"`,
		// The rest, once each, escaped.
		`<p class="hint" id="not-addresses">Not website addresses, to look up and paste by hand:</p>`,
		"<li>Gopher Days</li><li>the long feild</li><li>&lt;b&gt;Gopher&lt;/b&gt; &amp; co</li>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the filled-in form lacks %q", want)
		}
	}
	for _, unwanted := range []string{"unselected.example", "<b>Gopher</b>", "gopher days"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("the filled-in form has %q", unwanted)
		}
	}

	// Nobody who answered: the form, empty, says so.
	rr = b.post(t, "/seed/add/waitlist", url.Values{"id": {entryID(3)}})
	if body := rr.Body.String(); rr.Code != http.StatusOK || !strings.Contains(body, "None of the people selected named any sites.") || strings.Contains(body, "not-addresses") {
		t.Errorf("POST with no answers = %d, want 200 saying no one named any sites:\n%s", rr.Code, body)
	}
	// One entry.
	if body := b.post(t, "/seed/add/waitlist", url.Values{"id": {entryID(4)}}).Body.String(); !strings.Contains(body, "Sites from 1 waitlist entry.") {
		t.Error("the form doesn't say it's from 1 waitlist entry")
	}
}

func TestSeedAddWaitlistSelection(t *testing.T) {
	b := seedWaitlistServer(t, testWaitlist())

	rr := b.post(t, "/seed/add/waitlist", url.Values{})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/waitlist" {
		t.Fatalf("POST with no one selected = %d to %q, want 303 to /waitlist", rr.Code, rr.Header().Get("Location"))
	}
	if body := b.get(t, "/waitlist").Body.String(); !strings.Contains(body, "Select at least one person.") {
		t.Error("the waitlist doesn't flash that no one was selected")
	}

	many := url.Values{}
	for i := range maxWave + 1 {
		many.Add("id", entryID(i))
	}
	for name, form := range map[string]url.Values{
		"not a UUID": {"id": {entryID(1), "x"}},
		"too many":   many,
	} {
		if rr := b.post(t, "/seed/add/waitlist", form); rr.Code != http.StatusBadRequest {
			t.Errorf("POST %s = %d, want 400", name, rr.Code)
		}
	}
}

func TestSeedAddWaitlistFails(t *testing.T) {
	b := seedWaitlistServer(t, &fakeWaitlist{sitesErr: errors.New("boom")})
	if rr := b.post(t, "/seed/add/waitlist", url.Values{"id": {entryID(1)}}); rr.Code != http.StatusInternalServerError {
		t.Errorf("POST with the store failing = %d, want 500", rr.Code)
	}
}

func TestSplitWaitlistSites(t *testing.T) {
	sites, other := splitWaitlistSites([]string{"a.example, Name; b.example", "A.EXAMPLE\nname\nwww.b.example, c . example"})
	if want := []string{"a.example", "b.example", "c.example"}; !slices.Equal(sites, want) {
		t.Errorf("sites = %q, want %q", sites, want)
	}
	if want := []string{"Name"}; !slices.Equal(other, want) {
		t.Errorf("not addresses = %q, want %q", other, want)
	}
	if sites, other := splitWaitlistSites(nil); sites != nil || other != nil {
		t.Errorf("splitWaitlistSites(nil) = %q, %q, want none", sites, other)
	}
}
