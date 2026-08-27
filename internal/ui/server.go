// Package ui implements Shoal's built-in server-rendered web UI (no JS
// framework) so operators can manage devices/provisioning without NetBox.
//
// PLACEHOLDER SHELL: this file, layout.html, and the login/session/auth
// middleware below are scaffolding written against the documented contract
// from the parallel "UI shell" work unit, which had not landed in this
// worktree at the time this package was written (see the batch coordinator
// notes). The real shell PR owns Server's final shape, layout.html, a device
// list/nav page, and real session auth; at merge time this file should be
// replaced by that PR's version, keeping only the registerStatusRoutes call
// (see status.go) and the Server fields this unit reads/writes:
//
//	Directory, Observe, Jobs, JobStarter, JobCanceler, Profiles, Credentials, Power
//
// Everything else in this file (the login form, the in-memory session
// cookie, the layout template) exists solely so `go build`/`go test` and the
// E2E recipe in this unit's task description can run standalone.
package ui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mattcburns/shoal/internal/api"
	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/core/profile"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
	"github.com/mattcburns/shoal/internal/observe"
)

//go:embed templates/*.html
var templatesFS embed.FS

// DeviceDirectory is the subset of internal/common/directory.Store this page
// needs (GetDevice, to prefill BMC endpoint / credential_ref / lifecycle
// defaults the way the NetBox plugin reads device custom fields). Defined
// locally, matching models.DeviceIdentity, because internal/common/directory
// is owned by a sibling unit and does not exist in this worktree; swap in the
// real directory.Store at merge time (its GetDevice signature matches).
type DeviceDirectory interface {
	GetDevice(ctx context.Context, id string) (models.DeviceIdentity, error)
}

// Server is the /ui/* HTTP surface. Field names/types mirror what
// internal/api/jobs.go, power.go, and credentials.go already call
// JobStarter/JobCanceler/DevicePower/DeviceCredentials so this package reuses
// those exact interfaces (and their request/response structs) rather than
// redeclaring them.
type Server struct {
	Directory   DeviceDirectory
	Observe     *observe.Service
	Jobs        jobstore.Store
	JobStarter  api.JobStarter
	JobCanceler api.JobCanceler
	Profiles    profile.Store
	Credentials api.DeviceCredentials
	Power       api.DevicePower

	// DefaultBMCUsername/DefaultBMCPassword mirror the orchestrator-wide
	// SHOAL_BMC_* env fallback internal/api/power.go applies when a request
	// and the stored credential are both empty. Optional; leave zero to skip.
	DefaultBMCUsername string
	DefaultBMCPassword string

	// AuthToken gates /ui/login (placeholder session auth; see package doc).
	// Defaults to SHOAL_API_TOKEN's value when constructed via New. Empty
	// disables login (every request is treated as authenticated) — fine for
	// a lab default, matching api.withAPIAuth's "empty = open" behavior.
	AuthToken string

	Log *slog.Logger

	mux      *http.ServeMux
	tmplOnce sync.Once
	tmplBase *template.Template

	sessMu sync.Mutex
	sess   map[string]struct{}
}

// New constructs a UI Server and registers routes.
func New(log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{
		Log:  log,
		mux:  http.NewServeMux(),
		sess: make(map[string]struct{}),
	}
	s.routes()
	return s
}

// Handler returns the root /ui/* handler with session-cookie auth applied.
func (s *Server) Handler() http.Handler {
	return s.withSessionAuth(s.mux)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /ui/login", s.handleLoginForm)
	s.mux.HandleFunc("POST /ui/login", s.handleLoginSubmit)
	s.registerStatusRoutes()
}

// --- placeholder session auth (see package doc) ---

const sessionCookie = "shoal_ui_session"

func (s *Server) withSessionAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(s.AuthToken) == "" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/ui/login" {
			next.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie(sessionCookie)
		if err != nil || !s.validSession(c.Value) {
			http.Redirect(w, r, "/ui/login?next="+template.URLQueryEscaper(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) validSession(tok string) bool {
	if tok == "" {
		return false
	}
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	_, ok := s.sess[tok]
	return ok
}

func (s *Server) newSession() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	tok := hex.EncodeToString(b)
	s.sessMu.Lock()
	s.sess[tok] = struct{}{}
	s.sessMu.Unlock()
	return tok
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><html><body>
<h1>Shoal login</h1>
<form method="post" action="/ui/login">
<input type="hidden" name="next" value="` + template.HTMLEscapeString(r.URL.Query().Get("next")) + `">
<input type="password" name="password" placeholder="API token" autofocus>
<button type="submit">Sign in</button>
</form>
</body></html>`))
}

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	pass := r.PostForm.Get("password")
	if strings.TrimSpace(s.AuthToken) == "" || subtle.ConstantTimeCompare([]byte(pass), []byte(s.AuthToken)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("invalid credentials"))
		return
	}
	tok := s.newSession()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(12 * time.Hour),
	})
	next := r.PostForm.Get("next")
	if next == "" || !strings.HasPrefix(next, "/ui/") {
		next = "/ui/devices"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// --- template rendering ---

// renderPage executes templates/layout.html plus templates/<name> (which must
// {{define "content"}}...{{end}}) and writes the result. name is the
// page-local template file, e.g. "status.html".
func (s *Server) renderPage(w http.ResponseWriter, r *http.Request, name string, data any) {
	s.tmplOnce.Do(func() {
		s.tmplBase = template.Must(template.New("layout").Funcs(templateFuncs).ParseFS(templatesFS, "templates/layout.html"))
	})
	t, err := s.tmplBase.Clone()
	if err != nil {
		s.Log.Error("ui: clone template", "err", err.Error())
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	t, err = t.ParseFS(templatesFS, "templates/"+name)
	if err != nil {
		s.Log.Error("ui: parse template", "name", name, "err", err.Error())
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		s.Log.Error("ui: render", "name", name, "err", err.Error())
	}
}

var templateFuncs = template.FuncMap{
	"truncate": func(n int, s string) string {
		if len(s) <= n {
			return s
		}
		return s[:n]
	},
}
