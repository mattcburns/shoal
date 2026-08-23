package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestAPIAuthProtectsNewTelemetryRoutes covers the N1-N3 routes explicitly —
// the auth gate is a blanket /v1/ prefix match (TestAPIAuthProtectsV1 already
// proves the mechanism), but each new route is checked individually so a
// future refactor that accidentally registers one outside /v1/ is caught.
func TestAPIAuthProtectsNewTelemetryRoutes(t *testing.T) {
	s := api.New(config.Config{APIToken: "secret-token"}, nil)
	for _, path := range []string{
		"/v1/devices/x/jobs",
		"/v1/devices/x/sensors",
		"/v1/jobs/x/log",
	} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		s.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s: want 401 without bearer, got %d", path, rr.Code)
		}

		rr = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer secret-token")
		s.Handler().ServeHTTP(rr, req)
		if rr.Code == http.StatusUnauthorized {
			t.Fatalf("%s: valid bearer still unauthorized", path)
		}
	}
}

func TestAPIAuthProtectsDevicePower(t *testing.T) {
	s := api.New(config.Config{APIToken: "secret-token"}, nil)
	body := strings.NewReader(`{"reset_type":"On","bmc_endpoint":"https://bmc"}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/devices/6/power", body)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 without bearer, got %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/devices/6/power", strings.NewReader(`{"reset_type":"On","bmc_endpoint":"https://bmc"}`))
	req.Header.Set("Authorization", "Bearer secret-token")
	s.Handler().ServeHTTP(rr, req)
	if rr.Code == http.StatusUnauthorized {
		t.Fatal("valid bearer still unauthorized")
	}
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
