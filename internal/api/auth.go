package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// withAPIAuth requires Authorization: Bearer <token> for /v1/* when APIToken is set.
// /healthz, /readyz, and /metrics stay open for probes and scrapers.
func (s *Server) withAPIAuth(next http.Handler) http.Handler {
	token := strings.TrimSpace(s.cfg.APIToken)
	if token == "" {
		return next
	}
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requiresAPIAuth(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		got := bearerToken(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error": "unauthorized",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requiresAPIAuth(path string) bool {
	return strings.HasPrefix(path, "/v1/")
}

func bearerToken(h string) string {
	const p = "Bearer "
	if len(h) < len(p) || !strings.EqualFold(h[:len(p)], p) {
		// Also accept exact "bearer " mixed case via TrimPrefix after lower — use prefix check.
		if len(h) >= 7 && strings.EqualFold(h[:7], "bearer ") {
			return strings.TrimSpace(h[7:])
		}
		return ""
	}
	return strings.TrimSpace(h[len(p):])
}
