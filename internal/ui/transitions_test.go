package ui

import (
	"io/fs"
	"regexp"
	"testing"
)

// transitions.js skips the view transition on Back and Forward from a
// pagereveal listener, which must be registered before the first render: a
// deferred or async script can miss the event, and the crossfade would then
// play after the browser's own swipe animation.
func TestTransitionsScriptBlocksRender(t *testing.T) {
	if _, err := fs.Stat(Static, "static/js/transitions.js"); err != nil {
		t.Fatal(err)
	}
	b, err := fs.ReadFile(Templates, "templates/partials/head.html")
	if err != nil {
		t.Fatal(err)
	}
	tag := regexp.MustCompile(`<script[^>]*js/transitions\.js[^>]*>`).Find(b)
	if tag == nil {
		t.Fatal(`app-head (partials/head.html) doesn't load js/transitions.js`)
	}
	if regexp.MustCompile(`\s(defer|async)\b`).Match(tag) {
		t.Errorf("app-head loads transitions.js with %s; it must be a classic script so it listens for pagereveal before the first render", tag)
	}
}
