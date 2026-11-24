package sanitize

import (
	"strings"
	"testing"
)

func TestHTMLCodeLanguage(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string // the <pre>'s data-lang, "" for none
	}{
		{"language- on code", `<pre><code class="language-go">x</code></pre>`, "Go"},
		{"language- on pre", `<pre class="language-python">x</pre>`, "Python"},
		{"highlight.js", `<pre><code class="hljs language-js">x</code></pre>`, "JavaScript"},
		{"lang-", `<pre class="lang-sh">x</pre>`, "Shell"},
		{"alias, any case", `<pre><code class="language-Golang">x</code></pre>`, "Go"},
		{"Hugo's data-lang on code", `<div class="highlight"><pre class="chroma"><code class="language-ts" data-lang="ts">x</code></pre></div>`, "TypeScript"},
		{"Rouge's wrapper", `<div class="language-ruby highlighter-rouge"><div class="highlight"><pre class="highlight"><code>x</code></pre></div></div>`, "Ruby"},
		{"GitHub's wrapper", `<div class="highlight highlight-source-rust"><pre>x</pre></div>`, "Rust"},
		{"SyntaxHighlighter", `<pre class="brush: php; title: ; notranslate">x</pre>`, "PHP"},
		{"data-language", `<pre data-language="yaml">x</pre>`, "YAML"},
		{"the nearest wins", `<div class="language-ruby"><pre><code class="language-go">x</code></pre></div>`, "Go"},
		{"no language", `<pre><code>x</code></pre>`, ""},
		{"plain text", `<pre><code class="language-text">x</code></pre>`, ""},
		{"unknown language", `<pre><code class="language-cobol">x</code></pre>`, ""},
		{"the feed's own data-lang on pre is replaced", `<pre data-lang="Python"><code class="language-go">x</code></pre>`, "Go"},
		{"the feed's own data-lang on pre with no known language is removed", `<pre data-lang="<b>hi</b>">x</pre>`, ""},
		{"too far up", `<div class="language-go"><div><div><pre>x</pre></div></div></div>`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(HTML(tt.raw, nil, "").HTML)
			if tt.want == "" {
				if strings.Contains(got, "data-lang") {
					t.Errorf("HTML(%q) = %q, want no data-lang", tt.raw, got)
				}
				return
			}
			if want := `<pre data-lang="` + tt.want + `">`; !strings.Contains(got, want) {
				t.Errorf("HTML(%q) = %q, want it to contain %q", tt.raw, got, want)
			}
			if strings.Contains(got, "class=") {
				t.Errorf("HTML(%q) = %q, want no classes", tt.raw, got)
			}
		})
	}
}

// Every display name gets through the policy, including those with
// characters a pattern could trip on (C++, C#).
func TestLanguageNamesAllowed(t *testing.T) {
	for token, name := range languages {
		got := string(HTML(`<pre class="language-`+token+`">x</pre>`, nil, "").HTML)
		if want := `data-lang="` + name + `"`; !strings.Contains(got, want) {
			t.Errorf("language-%s: got %q, want it to contain %q", token, got, want)
		}
	}
}
