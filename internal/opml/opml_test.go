package opml

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// exportFixture is a file as the app's own export (exportOPMLHandler in
// internal/server/settings.go) writes it.
const exportFixture = `<?xml version="1.0" encoding="UTF-8"?>
<opml version="2.0">
  <head>
    <title>rss subscriptions</title>
    <dateCreated>Fri, 23 Oct 2026 12:00:00 +0000</dateCreated>
  </head>
  <body>
    <outline type="rss" text="Example Posts" title="Example Posts" xmlUrl="https://example.com/feed.xml" htmlUrl="https://example.com/"></outline>
    <outline type="rss" text="https://other.example.org/atom" xmlUrl="https://other.example.org/atom" htmlUrl="https://other.example.org/"></outline>
  </body>
</opml>
`

func TestParse(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []Outline
	}{
		{
			name:  "flat export",
			input: exportFixture,
			want: []Outline{
				{URL: "https://example.com/feed.xml", Title: "Example Posts"},
				{URL: "https://other.example.org/atom", Title: "https://other.example.org/atom"},
			},
		},
		{
			name: "nested folders",
			input: `<opml version="1.0"><body>
				<outline text="Tech">
					<outline text="A" xmlUrl="https://a.example.com/feed"/>
					<outline title="AI" text="Artificial intelligence">
						<outline text="B" xmlUrl="https://b.example.com/feed"></outline>
						<outline text="Tech"><outline text="C" xmlUrl="https://c.example.com/feed"/></outline>
					</outline>
					<outline text="D" xmlUrl="https://d.example.com/feed"/>
				</outline>
				<outline text="   ">
					<outline text="E" xmlUrl="https://e.example.com/feed"/>
				</outline>
				<outline text="Empty"/>
				<outline text="  News   today ">
					<outline text="F" xmlUrl="https://f.example.com/feed"/>
				</outline>
				<outline text="G" xmlUrl="https://g.example.com/feed"/>
			</body></opml>`,
			want: []Outline{
				{URL: "https://a.example.com/feed", Title: "A", Folders: []string{"Tech"}},
				{URL: "https://b.example.com/feed", Title: "B", Folders: []string{"Tech", "AI"}},
				{URL: "https://c.example.com/feed", Title: "C", Folders: []string{"Tech", "AI"}},
				{URL: "https://d.example.com/feed", Title: "D", Folders: []string{"Tech"}},
				{URL: "https://e.example.com/feed", Title: "E"},
				{URL: "https://f.example.com/feed", Title: "F", Folders: []string{"News today"}},
				{URL: "https://g.example.com/feed", Title: "G"},
			},
		},
		{
			name: "a URL in several folders is one outline with all of them",
			input: `<opml><body>
				<outline text="Tech"><outline text="First" xmlUrl="https://example.com/feed"/></outline>
				<outline text="Other" xmlUrl="https://other.example.com/feed"/>
				<outline text="News">
					<outline text="Second" xmlUrl="https://example.com/feed"/>
					<outline text="Tech"><outline text="Third" xmlUrl="https://example.com/feed"/></outline>
				</outline>
				<outline text="Top" xmlUrl="https://example.com/feed"/>
			</body></opml>`,
			want: []Outline{
				{URL: "https://example.com/feed", Title: "First", Folders: []string{"Tech", "News"}},
				{URL: "https://other.example.com/feed", Title: "Other"},
			},
		},
		{
			name: "outlines nested in a feed outline",
			input: `<opml><body>
				<outline text="Feed" xmlUrl="https://example.com/feed">
					<outline text="Child" xmlUrl="https://child.example.com/feed"/>
				</outline>
				<outline text="After" xmlUrl="https://after.example.com/feed"/>
			</body></opml>`,
			want: []Outline{
				{URL: "https://example.com/feed", Title: "Feed"},
				{URL: "https://child.example.com/feed", Title: "Child"},
				{URL: "https://after.example.com/feed", Title: "After"},
			},
		},
		{
			name: "outlines without xmlUrl are skipped",
			input: `<opml><body>
				<outline text="Folder"/>
				<outline type="link" text="A link" url="https://example.com/page"/>
				<outline text="Blank" xmlUrl="   "/>
				<outline text="Feed" xmlUrl="https://example.com/feed"/>
			</body></opml>`,
			want: []Outline{{URL: "https://example.com/feed", Title: "Feed"}},
		},
		{
			name: "a duplicate URL is dropped",
			input: `<opml><body>
				<outline text="First" xmlUrl="https://example.com/feed"/>
				<outline text="Folder"><outline text="Second" xmlUrl=" https://example.com/feed "/></outline>
				<outline text="Other" xmlUrl="https://example.com/feed?x=1"/>
			</body></opml>`,
			want: []Outline{
				{URL: "https://example.com/feed", Title: "First", Folders: []string{"Folder"}},
				{URL: "https://example.com/feed?x=1", Title: "Other"},
			},
		},
		{
			name:  "lowercase xmlurl",
			input: `<OPML><body><outline text="Feed" xmlurl="https://example.com/feed"/></body></OPML>`,
			want:  []Outline{{URL: "https://example.com/feed", Title: "Feed"}},
		},
		{
			name: "title falls back to text, whitespace collapsed",
			input: `<opml><body>
				<outline text="  Only
					text  " xmlUrl="https://a.example.com/feed"/>
				<outline text="Text" title="  The   title " xmlUrl="https://b.example.com/feed"/>
				<outline text="Text" title="  " xmlUrl="https://c.example.com/feed"/>
				<outline xmlUrl="https://d.example.com/feed"/>
			</body></opml>`,
			want: []Outline{
				{URL: "https://a.example.com/feed", Title: "Only text"},
				{URL: "https://b.example.com/feed", Title: "The title"},
				{URL: "https://c.example.com/feed", Title: "Text"},
				{URL: "https://d.example.com/feed", Title: ""},
			},
		},
		{
			name:  "ISO-8859-1",
			input: "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?>\n<opml><body><outline text=\"Caf\xe9\" xmlUrl=\"https://example.com/feed\"/></body></opml>",
			want:  []Outline{{URL: "https://example.com/feed", Title: "Café"}},
		},
		{
			name:  "unescaped ampersand and HTML entities",
			input: `<opml><body><outline text="Tom & Jerry&nbsp;&eacute;" xmlUrl="https://example.com/feed?a=1&b=2"/></body></opml>`,
			want:  []Outline{{URL: "https://example.com/feed?a=1&b=2", Title: "Tom & Jerry é"}},
		},
		{
			name:  "empty body",
			input: `<?xml version="1.0"?><opml version="2.0"><head><title>Empty</title></head><body></body></opml>`,
			want:  []Outline{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(strings.NewReader(tt.input))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse = %#v; want %#v", got, tt.want)
			}
		})
	}
}

func TestParseNotOPML(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"HTML page", `<!DOCTYPE html><html><head><title>Hi</title></head><body><p>Hello</body></html>`},
		{"RSS feed", `<?xml version="1.0"?><rss version="2.0"><channel><title>Feed</title></channel></rss>`},
		{"empty input", ``},
		{"plain text", `just some words`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(strings.NewReader(tt.input))
			if !errors.Is(err, ErrNotOPML) {
				t.Errorf("Parse = %v, %v; want ErrNotOPML", got, err)
			}
		})
	}
}
