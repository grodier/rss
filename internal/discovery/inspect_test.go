package discovery

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func readPageFixture(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/page.html")
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestInspectPageTags(t *testing.T) {
	insp := InspectPage(mustURL(t, "https://example.com/news/an-article"), readPageFixture(t))

	want := []PageTag{
		{Tag: "meta", Key: "charset", Value: "utf-8"},
		{Tag: "title", Value: "An Article | Example News", Used: true},
		{Tag: "base", Value: "https://cdn.example.net/assets/", Used: true},
		{Tag: "meta", Key: "og:title", Value: "An Article", Used: true},
		{Tag: "meta", Key: "description", Value: "What the article says.", Used: true},
		{Tag: "meta", Key: "og:image", Value: "/img/lead.jpg", Used: true},
		{Tag: "meta", Key: "article:published_time", Value: "2026-10-20T08:00:00Z"},
		{Tag: "meta", Key: "article:author", Value: "Ada Lovelace"},
		{Tag: "meta", Key: "twitter:card", Value: "summary_large_image"},
		{Tag: "link", Key: "canonical", Value: "https://example.com/news/an-article"},
		{Tag: "link", Key: "alternate", Value: "https://cdn.example.net/feed.xml", Extra: "application/rss+xml", Used: true},
		{Tag: "link", Key: "alternate", Value: "https://example.com/fr/un-article"},
		{Tag: "link", Key: "icon", Value: "https://cdn.example.net/assets/icon.png", Extra: "image/png 192x192", Used: true},
	}
	if !reflect.DeepEqual(insp.Tags, want) {
		t.Errorf("Tags =\n%s\nwant\n%s", formatTags(insp.Tags), formatTags(want))
	}
}

func formatTags(tags []PageTag) string {
	var b strings.Builder
	for _, t := range tags {
		b.WriteString("  ")
		b.WriteString(strings.Join([]string{t.Tag, t.Key, t.Value, t.Extra}, " | "))
		if t.Used {
			b.WriteString(" (used)")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func TestInspectPageJSONLD(t *testing.T) {
	insp := InspectPage(mustURL(t, "https://example.com/news/an-article"), readPageFixture(t))

	if want := []string{"NewsArticle", "WebPage", "ItemPage", "Organization"}; !slices.Equal(insp.JSONLDTypes, want) {
		t.Errorf("JSONLDTypes = %q, want %q", insp.JSONLDTypes, want)
	}
	if len(insp.JSONLD) != 3 {
		t.Fatalf("JSONLD has %d blocks, want 3 (not the plain script): %q", len(insp.JSONLD), insp.JSONLD)
	}
	if !strings.Contains(insp.JSONLD[0], "\n  \"@graph\": [\n") {
		t.Errorf("block 0 is not indented: %s", insp.JSONLD[0])
	}
	if insp.JSONLD[2] != "{not json" {
		t.Errorf("invalid block = %q, want it as written", insp.JSONLD[2])
	}
}

func TestInspectPageInfo(t *testing.T) {
	pageURL := mustURL(t, "https://example.com/news/an-article")
	body := readPageFixture(t)
	if got, want := InspectPage(pageURL, body).Info, ParsePage(pageURL, body); !reflect.DeepEqual(got, want) {
		t.Errorf("Info = %+v, want ParsePage's %+v", got, want)
	}
}

func TestInspectPageCuts(t *testing.T) {
	long := strings.Repeat("é", tagValueMax+10)
	bigJSON := `{"@type": "Thing", "x": "` + strings.Repeat("a", jsonLDMax) + `"}`
	body := `<meta name="keywords" content="` + long + `">` +
		`<script type="application/ld+json">` + bigJSON + `</script>`
	insp := InspectPage(mustURL(t, "https://example.com/"), []byte(body))

	if len(insp.Tags) != 1 || utf8.RuneCountInString(insp.Tags[0].Value) != tagValueMax {
		t.Errorf("Tags = %+v, want one value of %d runes", insp.Tags, tagValueMax)
	}
	if len(insp.JSONLD) != 1 || len(insp.JSONLD[0]) > jsonLDMax {
		t.Errorf("JSON-LD block is %d bytes, want at most %d", len(insp.JSONLD[0]), jsonLDMax)
	}
	if !slices.Equal(insp.JSONLDTypes, []string{"Thing"}) {
		t.Errorf("JSONLDTypes = %q, want the type of the whole block", insp.JSONLDTypes)
	}
}

func TestInspectPageUsedDuplicate(t *testing.T) {
	body := `<meta property="og:title" content="First"><meta property="og:title" content="Second">`
	insp := InspectPage(mustURL(t, "https://example.com/"), []byte(body))
	if len(insp.Tags) != 2 || !insp.Tags[0].Used || !insp.Tags[1].Used {
		t.Errorf("Tags = %+v, want both og:title tags marked used", insp.Tags)
	}
}
