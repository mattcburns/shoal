package ui_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/telemetry"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
	"github.com/mattcburns/shoal/internal/observe"
	"github.com/mattcburns/shoal/internal/ui"
)

func testLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestEventsEmptyState(t *testing.T) {
	store := jobstore.NewMemory()
	telem := telemetry.NewMemory()
	obs := observe.New(testLog(), store, telem, nil)
	srv := ui.New(testLog())
	srv.Observe = obs
	srv.Jobs = store

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/devices/dev-1/events", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"Time", "Severity", "Type", "Component", "Message", "No events yet"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q; body=%s", want, body)
		}
	}
}

func TestEventsWithRows(t *testing.T) {
	store := jobstore.NewMemory()
	telem := telemetry.NewMemory()
	obs := observe.New(testLog(), store, telem, nil)
	ctx := context.Background()
	if err := telem.WriteEvent(ctx, models.NormalizedEvent{
		DeviceID:  "dev-1",
		EventType: "sel",
		Severity:  "Critical",
		Component: "PSU1",
		Message:   "power supply failure",
		Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	srv := ui.New(testLog())
	srv.Observe = obs
	srv.Jobs = store

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/devices/dev-1/events?limit=10", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"power supply failure", "PSU1", "sev-critical"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q; body=%s", want, body)
		}
	}
}

func TestEventsObserveNotConfigured(t *testing.T) {
	srv := ui.New(testLog())
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/devices/dev-1/events", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "observe not configured") {
		t.Errorf("body missing not-configured banner; body=%s", rr.Body.String())
	}
}
