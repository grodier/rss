package sanitize

import (
	"slices"
	"strings"
	"testing"
)

func TestHTMLOutline(t *testing.T) {
	long := strings.Repeat("Word ", 40) // 200 bytes

	tests := []struct {
		name string
		raw  string
		want []Heading
		// html, if set, is what the body must contain.
		html []string
	}{
		{
			name: "h2s and h3s in order",
			raw:  `<h2>One</h2><p>a</p><h3>Two</h3><p>b</p><h2>Three</h2>`,
			want: []Heading{
				{ID: "body-one", Text: "One", Level: 2},
				{ID: "body-two", Text: "Two", Level: 3},
				{ID: "body-three", Text: "Three", Level: 2},
			},
			html: []string{`<h2 id="body-one">One</h2>`, `<h3 id="body-two">Two</h3>`, `<h2 id="body-three">Three</h2>`},
		},
		{
			name: "an id of its own keeps localAnchors' prefixed one",
			raw:  `<h2 id="intro">Intro</h2>`,
			want: []Heading{{ID: "body-intro", Text: "Intro", Level: 2}},
			html: []string{`<h2 id="body-intro">Intro</h2>`},
		},
		{
			name: "repeated text",
			raw:  `<h2>Notes</h2><h2>Notes</h2><h3>Notes</h3>`,
			want: []Heading{
				{ID: "body-notes", Text: "Notes", Level: 2},
				{ID: "body-notes-2", Text: "Notes", Level: 2},
				{ID: "body-notes-3", Text: "Notes", Level: 3},
			},
		},
		{
			name: "slug taken by another element's id",
			raw:  `<p id="notes">n</p><h2>Notes</h2>`,
			want: []Heading{{ID: "body-notes-2", Text: "Notes", Level: 2}},
			html: []string{`<p id="body-notes">n</p>`},
		},
		{
			name: "no letters or digits",
			raw:  `<h2>One</h2><h2>¿¡!?</h2>`,
			want: []Heading{
				{ID: "body-one", Text: "One", Level: 2},
				{ID: "body-section-2", Text: "¿¡!?", Level: 2},
			},
		},
		{
			name: "only heading without letters or digits",
			raw:  `<h2>¿¡!?</h2>`,
			want: []Heading{{ID: "body-section-1", Text: "¿¡!?", Level: 2}},
		},
		{
			name: "punctuation, capitals and accents",
			raw:  `<h2>  Step 1: Café &amp; <code>go build</code>!  </h2>`,
			want: []Heading{{ID: "body-step-1-caf-go-build", Text: "Step 1: Café & go build!", Level: 2}},
		},
		{
			name: "long heading",
			raw:  `<h2>` + long + `</h2>`,
			// The text cut to 97 bytes, then "…" (3 bytes).
			want: []Heading{{
				ID:    "body-" + strings.TrimSuffix(strings.Repeat("word-", 12), "-"),
				Text:  strings.TrimSpace(long)[:97] + "…",
				Level: 2,
			}},
		},
		{
			name: "long heading cut after a space",
			raw:  `<h2>` + strings.Repeat("a", 96) + ` bcdef</h2>`,
			want: []Heading{{ID: "body-" + strings.Repeat("a", 60), Text: strings.Repeat("a", 96) + "…", Level: 2}},
		},
		{
			name: "inline markup and whitespace in the text",
			raw:  "<h2>A <em>b</em>\n\t<a href=\"https://example.com/\">c</a></h2>",
			want: []Heading{{ID: "body-a-b-c", Text: "A b c", Level: 2}},
		},
		{
			name: "h1 demoted to h2",
			raw:  `<h1>Top</h1>`,
			want: []Heading{{ID: "body-top", Text: "Top", Level: 2}},
		},
		{
			name: "nested in wrappers",
			raw:  `<div><section><h2>Deep</h2></section></div>`,
			want: []Heading{{ID: "body-deep", Text: "Deep", Level: 2}},
		},
		{
			name: "in a blockquote",
			raw:  `<blockquote><h2>Quoted</h2></blockquote><h2>Own</h2>`,
			want: []Heading{{ID: "body-own", Text: "Own", Level: 2}},
			html: []string{`<blockquote><h2>Quoted</h2></blockquote>`},
		},
		{
			// socialCard's markup: the post's text is in a <blockquote>,
			// and nothing in the card is the article's.
			name: "in a post card",
			raw:  `<figure class="social-post" data-social="x"><figcaption><h3>Name</h3></figcaption></figure><h2>Own</h2>`,
			want: []Heading{{ID: "body-own", Text: "Own", Level: 2}},
		},
		{
			name: "in an element the policy removes with its content",
			raw:  `<object data="x.swf"><h2>Flash</h2></object><nostyle><h2>Unstyled</h2></nostyle><h2>Own</h2>`,
			want: []Heading{{ID: "body-own", Text: "Own", Level: 2}},
		},
		{
			name: "h4 and deeper",
			raw:  `<h4>Four</h4><h5>Five</h5><h6>Six</h6>`,
			html: []string{`<h4>Four</h4>`, `<h5>Five</h5>`, `<h6>Six</h6>`},
		},
		{
			name: "empty heading",
			raw:  `<h2> </h2><h3><img src="https://example.com/a.png" alt="A"></h3>`,
			html: []string{`<h2> </h2>`, `<h3><img`},
		},
		{
			name: "no headings",
			raw:  `<p>Text</p>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := HTML(tt.raw, nil, "")
			if !slices.Equal(body.Outline, tt.want) {
				t.Errorf("HTML(%q).Outline\n got: %+v\nwant: %+v", tt.raw, body.Outline, tt.want)
			}
			for _, s := range tt.html {
				if !strings.Contains(string(body.HTML), s) {
					t.Errorf("HTML(%q).HTML = %s; want it to contain %q", tt.raw, body.HTML, s)
				}
			}
			// Every heading listed is a fragment target in the body.
			for _, h := range body.Outline {
				if !strings.Contains(string(body.HTML), `id="`+h.ID+`"`) {
					t.Errorf("HTML(%q).HTML = %s; want an element with id %q", tt.raw, body.HTML, h.ID)
				}
			}
		})
	}
}
