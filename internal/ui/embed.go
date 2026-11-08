package ui

import "embed"

//go:embed templates/*.html templates/partials/*.html
var Templates embed.FS

// AdminTemplates are the admin's pages (admin/*.html) and the partials
// parsed with each (admin/partials/*.html).
//
//go:embed admin/*.html admin/partials/*.html
var AdminTemplates embed.FS

//go:embed emails/*.tmpl
var Emails embed.FS

//go:embed manifest.webmanifest
var Manifest []byte
