// Package ui provides Shoal's built-in, server-rendered web UI.
//
// This unit ("UI: Device Detail Events/Jobs tabs") implements the
// /ui/devices/{id}/events and /ui/devices/{id}/jobs routes in events.go and
// jobs.go, mirroring the read paths in internal/api/devices.go's
// handleDeviceEvents/handleDeviceJobs and internal/api/jobs.go's
// handleJobLog/handleCancelJob (calling Observe/Jobs/JobCanceler directly,
// in-process -- never via HTTP).
//
// Server, renderPage, and the layout template below are a MINIMAL
// PLACEHOLDER standing in for the sibling "UI shell" unit's own
// internal/ui/server.go + templates/layout.html, which were not yet merged
// when this unit was implemented. They exist only so this package builds
// and can be exercised standalone (go build/go vet/go test, and a local
// httptest server for the E2E check). The coordinator should reconcile this
// file and templates/layout.html against the real shell unit at merge time;
// events.go, jobs.go, templates/events.html, and templates/jobs.html are
// this unit's actual deliverable and should not need field-name changes
// beyond matching whatever the shell's Server struct turns out to call
// these fields.
//
// Assumed shell contract (documented in the task brief):
//   - Server has fields Observe *observe.Service, Jobs jobstore.Store, and a
//     JobCanceler-shaped field (named Canceler below; type JobCanceler
//     mirrors internal/api/jobs.go's JobCanceler interface).
//   - Cookie-based auth on /ui/* is applied by shell middleware; this
//     package does not implement or check auth itself.
//   - A renderPage(w, r, name string, data any) helper executes a base
//     layout template whose pages fill a {{define "content"}}...{{end}}
//     block.
package ui

import (
	"context"
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/mattcburns/shoal/internal/deploy/jobstore"
	"github.com/mattcburns/shoal/internal/observe"
)

//go:embed templates/*.html
var templateFS embed.FS

// JobCanceler cancels an in-flight job. Mirrors internal/api/jobs.go's
// JobCanceler interface exactly (internal/deploy/job.Orchestrator satisfies
// both).
type JobCanceler interface {
	Cancel(ctx context.Context, jobID string) error
}

// Server holds this package's handler dependencies. See the package doc
// comment above: this is a placeholder for the sibling shell unit's real
// Server type, which will likely carry additional fields (Directory, etc.)
// this unit's handlers do not use.
type Server struct {
	Observe  *observe.Service
	Jobs     jobstore.Store
	Canceler JobCanceler
	Log      *slog.Logger

	mux *http.ServeMux

	mu    sync.RWMutex
	pages map[string]*template.Template
}

// funcMap is available to every page template.
var funcMap = template.FuncMap{
	// truncate cuts s to at most n runes (not bytes), appending an ellipsis
	// when it does. Rune-based so multi-byte text (e.g. a non-ASCII BMC
	// error message) is never cut mid-character.
	"truncate": func(s string, n int) string {
		r := []rune(s)
		if len(r) <= n {
			return s
		}
		if n <= 1 {
			return string(r[:n])
		}
		return string(r[:n-1]) + "…"
	},
	"lower": strings.ToLower,
}

// New constructs a placeholder UI server and registers this unit's routes.
func New(log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{Log: log, pages: make(map[string]*template.Template)}
	s.mux = http.NewServeMux()
	s.routes()
	return s
}

// Handler returns the http.Handler serving this unit's routes. The real
// shell unit is expected to mount its own equivalent server at "/ui/" in
// internal/cli (out of scope here); this exists so the package can be
// exercised standalone (see server_test.go).
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /ui/devices/{id}/events", s.handleDeviceEvents)
	s.mux.HandleFunc("GET /ui/devices/{id}/jobs", s.handleDeviceJobs)
	s.mux.HandleFunc("POST /ui/devices/{id}/jobs", s.handleCancelJob)
}

// page returns the parsed layout+content template set for name (the content
// file's basename without ".html"), parsing and caching it on first use.
func (s *Server) page(name string) (*template.Template, error) {
	s.mu.RLock()
	t, ok := s.pages[name]
	s.mu.RUnlock()
	if ok {
		return t, nil
	}
	t, err := template.New("layout.html").Funcs(funcMap).ParseFS(templateFS, "templates/layout.html", "templates/"+name+".html")
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.pages[name] = t
	s.mu.Unlock()
	return t, nil
}

// renderPage executes the named content template inside the shared layout.
func (s *Server) renderPage(w http.ResponseWriter, r *http.Request, name string, data any) {
	t, err := s.page(name)
	if err != nil {
		s.Log.Error("ui render page", "name", name, "err", err.Error())
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout.html", data); err != nil {
		s.Log.Error("ui execute template", "name", name, "err", err.Error())
	}
}
