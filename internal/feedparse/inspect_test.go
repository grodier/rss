package feedparse

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func inspectTestdata(t *testing.T, name string) Inspection {
	t.Helper()
	insp, err := Inspect(mustParseURL(t, "https://example.com/feed"), readTestdata(t, name))
	if err != nil {
		t.Fatalf("Inspect(%s): %v", name, err)
	}
	return insp
}

// findField returns the field with path, failing the test if there is none.
func findField(t *testing.T, fields []Field, path string) Field {
	t.Helper()
	for _, f := range fields {
		if f.Path == path {
			return f
		}
	}
	t.Fatalf("no field %q in %+v", path, fields)
	return Field{}
}

// checkField checks a field's count and whether it's used.
func checkField(t *testing.T, fields []Field, path string, count int, used bool) {
	t.Helper()
	f := findField(t, fields, path)
	if f.Count != count || f.Used != used {
		t.Errorf("%s: count %d, used %v; want %d, %v", path, f.Count, f.Used, count, used)
	}
}

func TestInspectRSS(t *testing.T) {
	insp := inspectTestdata(t, "rss2.xml")

	checkField(t, insp.ItemFields, "title", 3, true)
	checkField(t, insp.ItemFields, "guid", 2, true)
	checkField(t, insp.ItemFields, "guid@isPermaLink", 1, false)
	checkField(t, insp.ItemFields, "dc:creator", 1, false)
	checkField(t, insp.ItemFields, "category", 2, false)
	checkField(t, insp.ItemFields, "enclosure@type", 1, true)
	checkField(t, insp.ItemFields, "enclosure@length", 1, false)

	if got := findField(t, insp.ItemFields, "dc:creator").Sample; got != "Ada" {
		t.Errorf("dc:creator sample = %q, want Ada", got)
	}
	if got := findField(t, insp.ItemFields, "guid").Sample; got != "https://example.com/?p=1" {
		t.Errorf("guid sample = %q, want it whitespace-collapsed", got)
	}
	// The first category is short; the long one is the second item's, so the
	// sample is the first non-empty value.
	if got := findField(t, insp.ItemFields, "category").Sample; got != "Examples" {
		t.Errorf("category sample = %q, want Examples", got)
	}

	checkField(t, insp.Channel, "title", 1, true)
	checkField(t, insp.Channel, "atom:link@href", 1, true)
	checkField(t, insp.Channel, "atom:link@type", 1, false)
	for _, f := range insp.Channel {
		if f.Path == "item" || strings.HasPrefix(f.Path, "item/") {
			t.Errorf("channel lists item field %q", f.Path)
		}
	}
	// Document order.
	if got := insp.Channel[0].Path; got != "atom:link" {
		t.Errorf("first channel field = %q, want atom:link", got)
	}

	// By count descending, then path.
	for i := 1; i < len(insp.ItemFields); i++ {
		a, b := insp.ItemFields[i-1], insp.ItemFields[i]
		if a.Count < b.Count || (a.Count == b.Count && a.Path > b.Path) {
			t.Errorf("item fields out of order: %q (%d) before %q (%d)", a.Path, a.Count, b.Path, b.Count)
		}
	}
}

func TestInspectSampleLength(t *testing.T) {
	body := `<rss version="2.0"><channel><title>T</title><item><guid>1</guid><category>` +
		strings.Repeat("é", 200) + `</category></item></channel></rss>`
	insp, err := Inspect(mustParseURL(t, "https://example.com/feed"), []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	got := findField(t, insp.ItemFields, "category").Sample
	if n := utf8.RuneCountInString(got); n != sampleLen {
		t.Errorf("sample has %d runes, want %d", n, sampleLen)
	}
	if !utf8.ValidString(got) {
		t.Error("sample is not valid UTF-8")
	}
}

func TestInspectAtom(t *testing.T) {
	insp := inspectTestdata(t, "atom.xml")

	checkField(t, insp.ItemFields, "id", 3, true)
	checkField(t, insp.ItemFields, "title", 3, true)
	checkField(t, insp.ItemFields, "title@type", 1, true)
	checkField(t, insp.ItemFields, "link@rel", 2, true)
	checkField(t, insp.ItemFields, "link@href", 3, true)
	checkField(t, insp.Channel, "title", 1, true)
	checkField(t, insp.Channel, "link@rel", 1, true)
	checkField(t, insp.Channel, "id", 1, false)
	for _, f := range insp.Channel {
		if f.Path == "entry" || strings.HasPrefix(f.Path, "entry/") {
			t.Errorf("channel lists entry field %q", f.Path)
		}
	}
}

func TestInspectMediaGroup(t *testing.T) {
	insp := inspectTestdata(t, "atom-media-group.xml")

	checkField(t, insp.ItemFields, "media:group/media:thumbnail@url", 1, true)
	checkField(t, insp.ItemFields, "media:group/media:content@type", 1, true)
	checkField(t, insp.ItemFields, "media:group", 1, true) // it holds used fields
	checkField(t, insp.ItemFields, "yt:videoId", 1, false)
	checkField(t, insp.Channel, "yt:channelId", 1, false)
	if got, want := insp.Items[0].Item.ImageURL, "https://i.ytimg.com/vi/abc/hqdefault.jpg"; got != want {
		t.Errorf("Items[0].Item.ImageURL = %q, want %q", got, want)
	}
}

func TestInspectUnknownNamespace(t *testing.T) {
	insp := inspectTestdata(t, "rss2-unknown-ns.xml")

	checkField(t, insp.ItemFields, "{https://example.com/ns/x}mood", 1, false)
	checkField(t, insp.ItemFields, "{https://example.com/ns/x}mood@level", 1, false)
	checkField(t, insp.Channel, "{https://example.com/ns/x}rating", 1, false)
	// A known namespace gets its conventional prefix, whatever the document
	// calls it.
	checkField(t, insp.ItemFields, "media:thumbnail@url", 1, true)
}

func TestInspectJSONFeed(t *testing.T) {
	insp := inspectTestdata(t, "jsonfeed.json")

	checkField(t, insp.ItemFields, "id", 2, true)
	checkField(t, insp.ItemFields, "attachments[].url", 1, true)
	checkField(t, insp.ItemFields, "attachments[].mime_type", 1, true)
	checkField(t, insp.ItemFields, "authors[].name", 1, false)
	checkField(t, insp.Channel, "title", 1, true)
	checkField(t, insp.Channel, "feed_url", 1, false)
	for _, f := range insp.Channel {
		if strings.HasPrefix(f.Path, "items") {
			t.Errorf("channel lists item field %q", f.Path)
		}
	}
	if got := insp.Channel[0].Path; got != "version" {
		t.Errorf("first channel field = %q, want version", got)
	}
}

func TestInspectRDF(t *testing.T) {
	insp := inspectTestdata(t, "rss1-rdf.xml")

	if len(insp.Items) != 2 {
		t.Fatalf("got %d items, want 2", len(insp.Items))
	}
	checkField(t, insp.ItemFields, "title", 2, true)
	checkField(t, insp.ItemFields, "dc:date", 2, true)
	checkField(t, insp.ItemFields, "@rdf:about", 2, false)
	checkField(t, insp.Channel, "title", 1, true)
	if !strings.HasPrefix(insp.Items[0].Raw, "<item") || !strings.Contains(insp.Items[0].Raw, "<title>A</title>") {
		t.Errorf("Raw = %q, want the first item", insp.Items[0].Raw)
	}
}

func TestInspectRawItems(t *testing.T) {
	t.Run("rss", func(t *testing.T) {
		insp := inspectTestdata(t, "rss2.xml")
		raw := insp.Items[0].Raw
		if !strings.HasPrefix(raw, "<item>") || !strings.HasSuffix(raw, "</item>") || !strings.Contains(raw, "First post") {
			t.Errorf("Raw = %q, want the first <item> element", raw)
		}
		if strings.Contains(raw, "No") && strings.Contains(raw, "guid  </title>") {
			t.Errorf("Raw holds the second item: %q", raw)
		}
	})

	t.Run("atom", func(t *testing.T) {
		insp := inspectTestdata(t, "atom.xml")
		raw := insp.Items[1].Raw
		if !strings.HasPrefix(raw, "<entry>") || !strings.Contains(raw, "A &amp; B") {
			t.Errorf("Raw = %q, want the second <entry> as written", raw)
		}
	})

	t.Run("json", func(t *testing.T) {
		insp := inspectTestdata(t, "jsonfeed.json")
		raw := insp.Items[0].Raw
		if !strings.HasPrefix(raw, "{\n  \"id\": \"1\",") {
			t.Errorf("Raw = %q, want the first item indented", raw)
		}
	})

	t.Run("gofeed", func(t *testing.T) {
		insp := inspectTestdata(t, "rss2.xml")
		var v struct{ Title string }
		if err := json.Unmarshal([]byte(insp.Items[0].Gofeed), &v); err != nil {
			t.Fatalf("Gofeed is not JSON: %v\n%s", err, insp.Items[0].Gofeed)
		}
		if v.Title != "First post" {
			t.Errorf("Gofeed title = %q, want First post", v.Title)
		}
	})
}

func TestInspectAlignment(t *testing.T) {
	for _, name := range []string{"rss2.xml", "rss2-self-only.xml", "rss-bom-cdata.xml", "rss1-rdf.xml", "atom.xml", "atom-no-rel.xml", "atom-media-group.xml", "jsonfeed.json", "rss2-unknown-ns.xml"} {
		t.Run(name, func(t *testing.T) {
			u := mustParseURL(t, "https://example.com/feed")
			body := readTestdata(t, name)
			feed, err := Parse(u, body)
			if err != nil {
				t.Fatal(err)
			}
			insp, err := Inspect(u, body)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(insp.Feed, feed) {
				t.Errorf("Inspect's Feed differs from Parse's")
			}
			if len(insp.Items) != len(feed.Items) {
				t.Fatalf("%d inspected items, want %d", len(insp.Items), len(feed.Items))
			}
			for i, it := range insp.Items {
				if !reflect.DeepEqual(it.Item, feed.Items[i]) {
					t.Errorf("item %d = %+v, want %+v", i, it.Item, feed.Items[i])
				}
				if it.Raw == "" {
					t.Errorf("item %d has no Raw", i)
				}
			}
		})
	}
}

func TestInspectNotFeed(t *testing.T) {
	_, err := Inspect(mustParseURL(t, "https://example.com/"), readTestdata(t, "page.html"))
	if !errors.Is(err, ErrNotFeed) {
		t.Errorf("err = %v, want ErrNotFeed", err)
	}
}
