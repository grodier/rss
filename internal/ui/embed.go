package ui

import "embed"

//go:embed templates/*.html templates/partials/*.html
var Templates embed.FS

//go:embed static
var Static embed.FS

//go:embed emails/*.tmpl
var Emails embed.FS

//go:embed manifest.webmanifest
var Manifest []byte
