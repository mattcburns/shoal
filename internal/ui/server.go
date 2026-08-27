// Package ui implements shoal's built-in, server-rendered web UI so
// operators can inspect/manage devices without NetBox.
//
// NOTE: this file is a minimal placeholder for the "UI shell" sibling unit
// in this batch of parallel work items, which owns internal/ui's real
// Server (cookie-auth middleware, device list/detail scaffolding, base
// layout, and a renderPage helper) per the batch's documented contract. It
// exists only so this unit's Sensors/Firmware tabs compile and are
// independently testable; reconcile field names/helpers with the shell
// unit at merge time. In particular the documented `Directory
// directory.Store` field is intentionally omitted here: internal/common/
// directory is a sibling unit and does not exist yet in this branch, and
// the Sensors/Firmware tabs don't need it (they key everything off the
// {id} path segment).
package ui

import (
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/mattcburns/shoal/internal/observe"
)

//go:embed templates/*.html
var templateFS embed.FS

// maxListLimit bounds the ?limit= query param on the sensors/firmware
// listings. Mirrors internal/api/server.go's maxListLimit (kept as an
// independent constant rather than importing internal/api, which is owned
// by a sibling unit in this batch).
const maxListLimit = 200

// Server is shoal's built-in web UI surface for the Sensors/Firmware device
// tabs implemented by this unit.
type Server struct {
	// Observe backs the Sensors/Firmware tabs' reads. Nil means "not
	// configured" and is rendered as a banner, not a 5xx.
	Observe *observe.Service
	// Poll runs the on-demand "Poll BMC" action in-process, mirroring
	// internal/api/poll.go's DevicePoll (see poll.go in this package).
	Poll DevicePoll
	// Log receives server-side detail for failures that must not reach the
	// browser verbatim, mirroring internal/api/errors.go's
	// writeUpstreamError. Defaults to slog.Default() when nil.
	Log *slog.Logger

	mux   *http.ServeMux
	pages map[string]*template.Template
}

// NewServer constructs a Server with the Sensors/Firmware routes registered.
func NewServer(obs *observe.Service, poll DevicePoll, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{
		Observe: obs,
		Poll:    poll,
		Log:     log,
		mux:     http.NewServeMux(),
	}
	s.pages = mustLoadPages()
	s.routes()
	return s
}

// Handler returns the handler for the /ui/devices/{id}/{sensors,firmware}
// routes this unit owns. Cookie-based auth is applied by the shell's
// middleware ahead of this handler in the real deployment; this standalone
// Handler applies none, matching "you don't need to handle auth yourself."
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /ui/devices/{id}/sensors", s.handleSensorsGet)
	s.mux.HandleFunc("POST /ui/devices/{id}/sensors", s.handleSensorsPoll)
	s.mux.HandleFunc("GET /ui/devices/{id}/firmware", s.handleFirmwareGet)
	s.mux.HandleFunc("POST /ui/devices/{id}/firmware", s.handleFirmwarePoll)
}

// mustLoadPages parses the shared base layout together with each page's
// `{{define "content"}}` template into its own independent template set (one
// per page), so pages don't collide on the "content" template name.
func mustLoadPages() map[string]*template.Template {
	pages := map[string]*template.Template{}
	for _, name := range []string{"sensors.html", "firmware.html"} {
		base := template.Must(template.New("layout").ParseFS(templateFS, "templates/layout.html"))
		pages[name] = template.Must(template.Must(base.Clone()).ParseFS(templateFS, "templates/"+name))
	}
	return pages
}

// renderPage renders name (a key in mustLoadPages) inside the shared base
// layout, mirroring the documented shell helper
// `renderPage(w, r, name string, data any)`.
func (s *Server) renderPage(w http.ResponseWriter, r *http.Request, name string, data any) {
	t, ok := s.pages[name]
	if !ok {
		http.Error(w, "template not found: "+name, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		s.logErr("ui render page", err, "page", name, "path", r.URL.Path)
	}
}

// logErr logs a failure server-side without leaking it into an HTML
// response, mirroring internal/api/errors.go's writeUpstreamError.
func (s *Server) logErr(msg string, err error, args ...any) {
	log := s.Log
	if log == nil {
		log = slog.Default()
	}
	all := make([]any, 0, len(args)+2)
	all = append(all, "err", err.Error())
	all = append(all, args...)
	log.Error(msg, all...)
}

// isNotConfiguredErr mirrors internal/api/errors.go's helper of the same
// name: it distinguishes "optional dependency isn't wired up" from "a
// configured dependency failed at call time."
func isNotConfiguredErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "not configured")
}

// parseLimit mirrors internal/api/devices.go's helper of the same name.
func parseLimit(r *http.Request, def, max int) int {
	limit := def
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > max {
		limit = max
	}
	return limit
}
