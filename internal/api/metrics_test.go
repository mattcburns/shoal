package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattcburns/shoal/internal/api"
	"github.com/mattcburns/shoal/internal/common/config"
)

func TestMetricsEndpoint(t *testing.T) {
	api.ResetMetricsForTest()
	s := api.New(config.Config{}, nil)

	// Generate a request so the counter increments.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	s.Handler().ServeHTTP(rr, req)

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"shoal_http_requests_total",
		"shoal_jobs_started_total",
		"shoal_jobs_cancel_total",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q:\n%s", want, body)
		}
	}
	if !strings.Contains(body, "shoal_http_requests_total ") {
		t.Fatalf("expected counter line, body=%s", body)
	}
}
