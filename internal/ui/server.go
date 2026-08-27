package ui

import (
	"bytes"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/mattcburns/shoal/internal/api"
	"github.com/mattcburns/shoal/internal/common/directory"
	"github.com/mattcburns/shoal/internal/common/secrets"
	"github.com/mattcburns/shoal/internal/core/profile"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
	"github.com/mattcburns/shoal/internal/observe"
)

// Config carries every dependency ui.New needs to build a Server. Fields not
// used by this unit's pages (Devices list/add/edit + shell) are placeholders
// for units 6-8's device-detail tabs (Status/Events/Jobs/Sensors/Firmware),
// documented here so the Server struct shape stays stable across those PRs.
type Config struct {
	// Directory is the device-identity backend (file-backed or NetBox-backed,
	// selected at runtime -- never a build tag). Used by the Device List/Add/
	// Edit/Detail pages this unit implements.
	Directory directory.Store

	// Observe is the aggregate device-status/event/job-log service
	// (internal/observe). Placeholder for unit 6/7's Status/Events/Jobs tabs;
	// unused by this unit.
	Observe *observe.Service

	// Jobs is the durable job store. Placeholder for unit 7's Jobs tab; unused
	// by this unit.
	Jobs jobstore.Store

	// JobStarter starts a provisioning job (Deploy Orchestrator). Placeholder
	// for a future "start job" UI action; unused by this unit.
	JobStarter api.JobStarter

	// JobCanceler cancels an in-flight job (Deploy Orchestrator). Placeholder
	// for a future job-cancel UI action; unused by this unit.
	JobCanceler api.JobCanceler

	// Profiles is the saved-provisioning-profile store. Placeholder for a
	// future profile_ref picker on a Provision form; unused by this unit's
	// plain Add Device form.
	Profiles profile.Store

	// Secrets resolves/stores BMC credentials (credential_ref -> username/
	// password). Placeholder for a future credentials-edit UI action; unused
	// by this unit (the Add/Edit Device form only writes credential_ref, an
	// opaque string, never a raw secret).
	Secrets secrets.Backend

	// Power dials a BMC to apply a Redfish reset (On/ForceOff/ForceRestart),
	// the same api.DevicePower surface internal/api/power.go uses. Placeholder
	// for operator power-control buttons on the device detail page in a later
	// unit; unused by this unit.
	Power api.DevicePower

	// APIToken gates /ui/* behind a login cookie when non-empty (compared
	// against the POSTed password on /ui/login) -- the same value as
	// SHOAL_API_TOKEN / internal/api's Bearer auth. Empty disables UI auth
	// entirely, mirroring internal/api/auth.go's "auth disabled when token
	// unset" behavior. Passed in by the caller (cmdServe); this package never
	// reads the environment itself.
	APIToken string

	// Log receives handler/template errors. Defaults to slog.Default() if nil.
	Log *slog.Logger
}

// Server is the built-in web UI's HTTP surface: its own http.ServeMux and
// Handler() method, mounted by internal/cli/cli.go's cmdServe as a sibling of
// the existing API mux (see cmdServe for how the two are combined under one
// http.Server).
type Server struct {
	mux      *http.ServeMux
	log      *slog.Logger
	apiToken string

	// Directory backs the Device List/Add/Edit/Detail pages. See Config.Directory.
	Directory directory.Store
	// Observe is unused by this unit. See Config.Observe.
	Observe *observe.Service
	// Jobs is unused by this unit. See Config.Jobs.
	Jobs jobstore.Store
	// JobStarter is unused by this unit. See Config.JobStarter.
	JobStarter api.JobStarter
	// JobCanceler is unused by this unit. See Config.JobCanceler.
	JobCanceler api.JobCanceler
	// Profiles is unused by this unit. See Config.Profiles.
	Profiles profile.Store
	// Secrets is unused by this unit. See Config.Secrets.
	Secrets secrets.Backend
	// Power is unused by this unit. See Config.Power.
	Power api.DevicePower
}

// New builds a Server with routes registered. This is the single constructor
// other code (cmdServe, tests) calls.
func New(cfg Config) *Server {
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	s := &Server{
		mux:         http.NewServeMux(),
		log:         log,
		apiToken:    cfg.APIToken,
		Directory:   cfg.Directory,
		Observe:     cfg.Observe,
		Jobs:        cfg.Jobs,
		JobStarter:  cfg.JobStarter,
		JobCanceler: cfg.JobCanceler,
		Profiles:    cfg.Profiles,
		Secrets:     cfg.Secrets,
		Power:       cfg.Power,
	}
	s.routes()
	return s
}

// Handler returns the root handler (auth middleware over the routed mux).
func (s *Server) Handler() http.Handler {
	return s.withUIAuth(s.mux)
}

func (s *Server) routes() {
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// static/*.css is embedded above; this can only fail if the embed
		// directive itself is broken, which build would already have caught.
		panic("ui: static assets: " + err.Error())
	}
	s.mux.Handle("GET /ui/static/", http.StripPrefix("/ui/static/", http.FileServerFS(staticSub)))

	s.mux.HandleFunc("GET /ui/login", s.handleLoginForm)
	s.mux.HandleFunc("POST /ui/login", s.handleLoginSubmit)
	s.mux.HandleFunc("POST /ui/logout", s.handleLogout)

	s.mux.HandleFunc("GET /ui/{$}", s.handleRoot)
	s.mux.HandleFunc("GET /ui/devices", s.handleDeviceList)
	s.mux.HandleFunc("GET /ui/devices/new", s.handleDeviceNewForm)
	s.mux.HandleFunc("POST /ui/devices/new", s.handleDeviceNewSubmit)
	s.mux.HandleFunc("GET /ui/devices/{id}", s.handleDeviceDetail)
	s.mux.HandleFunc("GET /ui/devices/{id}/edit", s.handleDeviceEditForm)
	s.mux.HandleFunc("POST /ui/devices/{id}/edit", s.handleDeviceEditSubmit)
	s.mux.HandleFunc("POST /ui/devices/{id}/delete", s.handleDeviceDelete)
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/ui/devices", http.StatusFound)
}

// renderPage executes the named content template (a file under
// internal/ui/templates/, e.g. "devices_list.html") inside the shared
// layout.html chrome and writes the result. name's file must define a
// `{{define "content"}}...{{end}}` template -- see embed.go's doc comment
// for the full convention. Units 6-8 call this exact helper (signature and
// behavior) from their own page handlers.
func (s *Server) renderPage(w http.ResponseWriter, r *http.Request, name string, data any) {
	tmpl, err := template.ParseFS(templatesFS, "templates/layout.html", "templates/"+name)
	if err != nil {
		s.log.Error("ui: render page: parse", "template", name, "err", err.Error())
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout.html", data); err != nil {
		s.log.Error("ui: render page: execute", "template", name, "err", err.Error())
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}
