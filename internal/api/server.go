// Package api implements net/http ServeMux handlers for the Shoal HTTP API.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/mattcburns/shoal/internal/common/config"
	"github.com/mattcburns/shoal/internal/common/telemetry"
	"github.com/mattcburns/shoal/internal/core/profile"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
	"github.com/mattcburns/shoal/internal/discover"
	"github.com/mattcburns/shoal/internal/observe"
)

// maxListLimit bounds the ?limit= query param on list endpoints (jobs-by-device,
// sensors, job log) so a caller can't force an unbounded scan/response.
const maxListLimit = 200

// Server is the HTTP API surface (health + jobs + discover + observe).
type Server struct {
	cfg config.Config
	log *slog.Logger
	mux *http.ServeMux
	// PingDB may be overridden in tests; defaults to telemetry.PingDB.
	PingDB   func(ctx context.Context, dsn string) error
	jobs     jobstore.Store
	cancel   JobCanceler
	start    JobStarter
	discover *discover.Service
	observe  *observe.Service
	power    DevicePower
	creds    DeviceCredentials
	poll     DevicePoll
	profiles profile.Store
}

// New constructs a Server with routes registered.
func New(cfg config.Config, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{
		cfg:    cfg,
		log:    log,
		mux:    http.NewServeMux(),
		PingDB: telemetry.PingDB,
	}
	s.routes()
	return s
}

// Handler returns the root handler (metrics + optional API auth wrappers).
func (s *Server) Handler() http.Handler {
	return s.withAPIAuth(withHTTPMetrics(s.mux))
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /readyz", s.handleReadyz)
	s.mux.HandleFunc("GET /metrics", s.handleMetrics)
	s.mux.HandleFunc("POST /v1/jobs", s.handleStartJob)
	s.mux.HandleFunc("GET /v1/jobs/{id}", s.handleGetJob)
	s.mux.HandleFunc("GET /v1/jobs/{id}/log", s.handleJobLog)
	s.mux.HandleFunc("POST /v1/jobs/{id}/cancel", s.handleCancelJob)
	s.mux.HandleFunc("POST /v1/discover/ingest", s.handleDiscoverIngest)
	s.mux.HandleFunc("POST /v1/discover/confirm", s.handleDiscoverConfirm)
	s.mux.HandleFunc("GET /v1/devices/{id}/status", s.handleDeviceStatus)
	s.mux.HandleFunc("GET /v1/devices/{id}/events", s.handleDeviceEvents)
	s.mux.HandleFunc("GET /v1/devices/{id}/jobs", s.handleDeviceJobs)
	s.mux.HandleFunc("GET /v1/devices/{id}/sensors", s.handleDeviceSensors)
	s.mux.HandleFunc("GET /v1/devices/{id}/firmware", s.handleDeviceFirmware)
	s.mux.HandleFunc("POST /v1/devices/{id}/power", s.handleDevicePower)
	s.mux.HandleFunc("POST /v1/devices/{id}/poll", s.handleDevicePoll)
	s.mux.HandleFunc("GET /v1/devices/{id}/credentials", s.handleDeviceCredentialsGet)
	s.mux.HandleFunc("PUT /v1/devices/{id}/credentials", s.handleDeviceCredentialsPut)
	s.mux.HandleFunc("GET /v1/profiles", s.handleListProfiles)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
	})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"status":   "ok",
		"database": "not_configured",
	}

	dsn := s.cfg.TelemetryDatabaseURL
	if dsn == "" {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	resp["database"] = "configured"
	ping := s.PingDB
	if ping == nil {
		ping = telemetry.PingDB
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := ping(ctx, dsn); err != nil {
		s.log.Warn("readyz database ping failed", "err", err.Error())
		resp["status"] = "not_ready"
		resp["database"] = "error"
		resp["error"] = "database unreachable"
		writeJSON(w, http.StatusServiceUnavailable, resp)
		return
	}
	resp["database"] = "ok"
	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
