package sanitize

import (
	"regexp"
	"slices"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// languages are the display names of the languages a code block's label
// can show, by the token feeds name them with (language-go, lang-sh,
// data-lang="golang"…). A token not here gets no label: plain text, and
// languages too rare to name well.
var languages = map[string]string{
	"asm": "Assembly", "nasm": "Assembly", "x86asm": "Assembly",
	"bash": "Shell", "console": "Shell", "sh": "Shell", "shell": "Shell", "shell-session": "Shell", "shellsession": "Shell", "sh-session": "Shell", "terminal": "Shell", "zsh": "Shell",
	"c":   "C",
	"c++": "C++", "cc": "C++", "cpp": "C++", "cxx": "C++",
	"c#": "C#", "cs": "C#", "csharp": "C#",
	"clj": "Clojure", "clojure": "Clojure",
	"css":  "CSS",
	"dart": "Dart",
	"diff": "Diff", "patch": "Diff",
	"docker": "Dockerfile", "dockerfile": "Dockerfile",
	"elixir": "Elixir", "ex": "Elixir", "exs": "Elixir",
	"elm": "Elm",
	"erl": "Erlang", "erlang": "Erlang",
	"fish": "Fish",
	"go":   "Go", "golang": "Go",
	"gql": "GraphQL", "graphql": "GraphQL",
	"haskell": "Haskell", "hs": "Haskell",
	"hcl": "HCL", "terraform": "HCL", "tf": "HCL",
	"htm": "HTML", "html": "HTML",
	"http":       "HTTP",
	"ini":        "INI",
	"java":       "Java",
	"javascript": "JavaScript", "js": "JavaScript", "jsx": "JavaScript", "mjs": "JavaScript",
	"json": "JSON", "jsonc": "JSON",
	"jl": "Julia", "julia": "Julia",
	"kotlin": "Kotlin", "kt": "Kotlin",
	"lua":  "Lua",
	"make": "Makefile", "makefile": "Makefile",
	"markdown": "Markdown", "md": "Markdown",
	"nginx": "Nginx",
	"nix":   "Nix",
	"objc":  "Objective-C", "objective-c": "Objective-C",
	"ml": "OCaml", "ocaml": "OCaml",
	"perl": "Perl", "pl": "Perl",
	"php":        "PHP",
	"powershell": "PowerShell", "ps1": "PowerShell", "pwsh": "PowerShell",
	"proto": "Protobuf", "protobuf": "Protobuf",
	"py": "Python", "python": "Python", "python3": "Python",
	"r":  "R",
	"rb": "Ruby", "ruby": "Ruby",
	"rs": "Rust", "rust": "Rust",
	"sass": "Sass", "scss": "SCSS",
	"scala":    "Scala",
	"postgres": "SQL", "postgresql": "SQL", "psql": "SQL", "sql": "SQL",
	"svelte": "Svelte",
	"swift":  "Swift",
	"toml":   "TOML",
	"ts":     "TypeScript", "tsx": "TypeScript", "typescript": "TypeScript",
	"vim":  "Vim script",
	"vue":  "Vue",
	"wasm": "WebAssembly", "wat": "WebAssembly",
	"svg": "XML", "xml": "XML",
	"yaml": "YAML", "yml": "YAML",
	"zig": "Zig",
}

// languageNames matches the display names in languages, for the policy:
// data-lang on a <pre> is only ever one of them.
var languageNames = func() *regexp.Regexp {
	var names []string
	for _, name := range languages {
		names = append(names, regexp.QuoteMeta(name))
	}
	slices.Sort(names)
	names = slices.Compact(names)
	return regexp.MustCompile(`^(` + strings.Join(names, "|") + `)$`)
}()

// classLanguage matches a class naming a language, as the common
// highlighters write it: language-go (Markdown renderers, Prism,
// highlight.js, Rouge's wrapper), lang-go, highlight-source-go (GitHub), and
// WordPress's SyntaxHighlighter's "brush: go;".
var classLanguage = regexp.MustCompile(`(?:^|\s)(?:language|lang|highlight-source)-(\S+)|brush:\s*([^\s;]+)`)

// labelCode sets data-lang on pre to the display name of the language the
// feed says it's written in (see codeLanguage), for its label, and removes
// any data-lang it came with.
func labelCode(pre *html.Node) {
	removeAttrs(pre, "data-lang")
	if name, ok := codeLanguage(pre); ok {
		setAttr(pre, "data-lang", name)
	}
}

// codeLanguage returns the display name of pre's language, read from the
// classes and data-lang or data-language of pre, its first <code>, or the
// two elements around it (highlighters wrap a block in a <div> or two), the
// nearest first. It reports false when none names a language in languages.
func codeLanguage(pre *html.Node) (string, bool) {
	nodes := []*html.Node{pre}
	for c := pre.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.DataAtom == atom.Code {
			nodes = []*html.Node{c, pre}
			break
		}
	}
	for p, i := pre.Parent, 0; p != nil && p.Type == html.ElementNode && i < 2; p, i = p.Parent, i+1 {
		nodes = append(nodes, p)
	}
	for _, n := range nodes {
		for _, key := range []string{"data-lang", "data-language"} {
			if v, ok := attr(n, key); ok {
				if name, ok := languages[strings.ToLower(strings.TrimSpace(v))]; ok {
					return name, true
				}
			}
		}
		class, _ := attr(n, "class")
		for _, m := range classLanguage.FindAllStringSubmatch(class, -1) {
			if name, ok := languages[strings.ToLower(m[1]+m[2])]; ok {
				return name, true
			}
		}
	}
	return "", false
}
