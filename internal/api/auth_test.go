package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattcburns/shoal/internal/api"
	"github.com/mattcburns/shoal/internal/common/config"
)

func TestAPIAuthOpenWhenTokenEmpty(t *testing.T) {
	s := api.New(config.Config{}, nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/jobs/x", nil)
	s.Handler().ServeHTTP(rr, req)
	// No store → 503, but not 401
	if rr.Code == http.StatusUnauthorized {
		t.Fatalf("unexpected 401 when token empty")
	}
}

func TestAPIAuthProtectsV1(t *testing.T) {
	s := api.New(config.Config{APIToken: "secret-token"}, nil)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/jobs/x", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 without bearer, got %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("healthz should stay open, got %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("metrics should stay open, got %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/jobs/x", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	s.Handler().ServeHTTP(rr, req)
	if rr.Code == http.StatusUnauthorized {
		t.Fatalf("valid bearer still unauthorized")
	}
	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
}

func TestAPIAuthRejectsWrongToken(t *testing.T) {
	s := api.New(config.Config{APIToken: "secret-token"}, nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/jobs/x", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
}
