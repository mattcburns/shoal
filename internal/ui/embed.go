// Package ui is the built-in server-rendered web UI (no JS framework): an
// operator can manage devices/provisioning without NetBox, which stays
// available as an optional alternative.
//
// Package layout mirrors internal/api: its own http.ServeMux, its own
// Handler() method, so internal/cli/cli.go's cmdServe can mount it as a
// sibling alongside the existing API mux.
//
// Templates/static assets are embedded the same way prompts/embed.go embeds
// prompt text files (//go:embed into a package-level embed.FS), adapted here
// for html/template pages and a stylesheet.
//
// Base layout convention (binding on every sub-page, including units 6-8's
// Status/Events/Jobs/Sensors/Firmware tabs added in later PRs): templates/
// layout.html is parsed together with exactly one page-specific template file
// per render. layout.html itself is the outer HTML document and contains
// `{{template "content" .}}` where the page body goes. Each page-specific
// file defines that hole with `{{define "content"}}...{{end}}` -- the block
// name is always literally "content". renderPage (server.go) is the only
// place that parses and executes this pair; page handlers never touch
// html/template directly.
package ui

import "embed"

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*.css
var staticFS embed.FS
