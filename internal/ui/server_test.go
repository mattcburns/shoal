package ui_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/api"
	"github.com/mattcburns/shoal/internal/common/telemetry"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
	"github.com/mattcburns/shoal/internal/observe"
	"github.com/mattcburns/shoal/internal/ui"
)

// fakePoll is an in-memory api.DevicePoll used by tests: it records the last
// request it received and returns a canned result/error.
type fakePoll struct {
	lastDeviceID string
	lastReq      api.DevicePollRequest
	result       api.DevicePollResult
	err          error
	called       bool
}

func (f *fakePoll) Poll(_ context.Context, deviceID string, req api.DevicePollRequest) (api.DevicePollResult, error) {
	f.called = true
	f.lastDeviceID = deviceID
	f.lastReq = req
	return f.result, f.err
}

func newTestServer(t *testing.T, store telemetry.Store, poll api.DevicePoll) *ui.Server {
	t.Helper()
	jobs := jobstore.NewMemory()
	obs := observe.New(nil, jobs, store, nil)
	return ui.New(ui.Config{Observe: obs, Poll: poll})
}

func TestSensorsGetEmptyState(t *testing.T) {
	store := telemetry.NewMemory()
	s := newTestServer(t, store, &fakePoll{})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/devices/dev-1/sensors", nil)
	s.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"Poll BMC", "bmc_endpoint", "bmc_username", "bmc_password", "Sensor", "Value", "Unit", "Note", "Time", "No sensor readings yet"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected body to contain %q; body=%s", want, body)
		}
	}
}

func TestSensorsGetWithReadings(t *testing.T) {
	store := telemetry.NewMemory()
	ctx := context.Background()
	if err := store.WriteSensor(ctx, telemetry.SensorReading{
		DeviceID: "dev-1", Sensor: "Fan1", Value: telemetry.SensorValue(3200), Unit: "RPM",
	}); err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, store, &fakePoll{})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/devices/dev-1/sensors?limit=10", nil)
	s.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Fan1") || !strings.Contains(body, "3200") || !strings.Contains(body, "RPM") {
		t.Fatalf("expected sensor row in body: %s", body)
	}
}

func TestSensorsGetObserveNotConfigured(t *testing.T) {
	jobs := jobstore.NewMemory()
	obs := observe.New(nil, jobs, nil, nil) // nil telemetry store -> "not configured"
	s := ui.New(ui.Config{Observe: obs, Poll: &fakePoll{}})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/devices/dev-1/sensors", nil)
	s.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "not configured") {
		t.Fatalf("expected not-configured banner: %s", rr.Body.String())
	}
}

func TestFirmwareGetEmptyState(t *testing.T) {
	store := telemetry.NewMemory()
	s := newTestServer(t, store, &fakePoll{})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/devices/dev-1/firmware", nil)
	s.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"Poll BMC", "bmc_endpoint", "Name", "Version", "Manufacturer", "Health", "Updateable", "Id", "No firmware inventory yet"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected body to contain %q; body=%s", want, body)
		}
	}
}

func TestFirmwareGetWithComponents(t *testing.T) {
	store := telemetry.NewMemory()
	ctx := context.Background()
	if err := store.WriteFirmware(ctx, telemetry.FirmwareComponent{
		DeviceID: "dev-1", ID: "BIOS.Setup.1-1", Name: "BIOS", Version: "2.1.0",
		Manufacturer: "Dell", Health: "OK", Updateable: true,
	}); err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, store, &fakePoll{})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/devices/dev-1/firmware", nil)
	s.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "BIOS") || !strings.Contains(body, "2.1.0") || !strings.Contains(body, "Dell") {
		t.Fatalf("expected firmware row in body: %s", body)
	}
	if !strings.Contains(body, "as of") {
		t.Fatalf("expected an 'as of' timestamp: %s", body)
	}
}

func TestPollFormRedirectsAndFillsSensors(t *testing.T) {
	store := telemetry.NewMemory()
	poll := &fakePoll{result: api.DevicePollResult{
		DeviceID: "dev-1", SELNew: 2, SensorsWritten: 5, FirmwareWritten: 3, PowerState: "On",
	}}
	s := newTestServer(t, store, poll)

	form := url.Values{}
	form.Set("bmc_endpoint", "https://10.0.0.9")
	form.Set("bmc_username", "root")
	form.Set("bmc_password", "hunter2")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/devices/dev-1/sensors", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !poll.called {
		t.Fatal("expected Poll to be called")
	}
	if poll.lastDeviceID != "dev-1" {
		t.Fatalf("device id=%s", poll.lastDeviceID)
	}
	if poll.lastReq.BMCEndpoint != "https://10.0.0.9" || poll.lastReq.BMCUsername != "root" || poll.lastReq.BMCPassword != "hunter2" {
		t.Fatalf("%+v", poll.lastReq)
	}

	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "/ui/devices/dev-1/sensors") {
		t.Fatalf("expected redirect back to sensors tab, got %s", loc)
	}

	// Follow the redirect and confirm the GET renders the poll outcome banner.
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, loc, nil)
	s.Handler().ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr2.Code, rr2.Body.String())
	}
	body := rr2.Body.String()
	if !strings.Contains(body, "5 sensor(s)") || !strings.Contains(body, "3 firmware") || !strings.Contains(body, "power=On") || !strings.Contains(body, "2 new SEL") {
		t.Fatalf("expected poll outcome banner: %s", body)
	}
	if !strings.Contains(body, `value="https://10.0.0.9"`) {
		t.Fatalf("expected bmc_endpoint to be re-populated: %s", body)
	}
}

func TestPollFormMissingEndpointRedirectsWithError(t *testing.T) {
	store := telemetry.NewMemory()
	poll := &fakePoll{}
	s := newTestServer(t, store, poll)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/devices/dev-1/firmware", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status=%d", rr.Code)
	}
	if poll.called {
		t.Fatal("Poll must not be called without a bmc_endpoint")
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "poll_err=1") {
		t.Fatalf("expected poll_err=1 in redirect: %s", loc)
	}
}

func TestPollFormBackendError(t *testing.T) {
	store := telemetry.NewMemory()
	poll := &fakePoll{err: errors.New("dial tcp 10.0.0.9:443: connection refused")}
	s := newTestServer(t, store, poll)

	form := url.Values{}
	form.Set("bmc_endpoint", "https://10.0.0.9")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/devices/dev-1/sensors", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status=%d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "poll_err=1") {
		t.Fatalf("expected poll_err=1: %s", loc)
	}

	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, loc, nil)
	s.Handler().ServeHTTP(rr2, req2)
	body := rr2.Body.String()
	// The raw backend error detail must never reach the rendered page (the
	// bmc_endpoint the user typed is legitimately re-populated in the form,
	// so only the error text itself is checked here).
	if strings.Contains(body, "connection refused") {
		t.Fatalf("raw backend error leaked into response: %s", body)
	}
	if !strings.Contains(body, "Poll BMC failed") {
		t.Fatalf("expected generic poll failure banner: %s", body)
	}
}

func TestPollNotConfigured(t *testing.T) {
	store := telemetry.NewMemory()
	s := newTestServer(t, store, nil)

	form := url.Values{}
	form.Set("bmc_endpoint", "https://10.0.0.9")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/devices/dev-1/firmware", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status=%d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "poll_err=1") {
		t.Fatalf("expected poll_err=1: %s", loc)
	}
}

func TestSensorsLimitClamped(t *testing.T) {
	store := telemetry.NewMemory()
	ctx := context.Background()
	// All rows share one explicit timestamp so ListSensors(since=zero), which
	// returns only the latest poll snapshot, includes every one of them.
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < maxListLimitForTest+10; i++ {
		if err := store.WriteSensor(ctx, telemetry.SensorReading{
			DeviceID: "dev-1", TS: ts, Sensor: fmt.Sprintf("s%03d", i), Value: telemetry.SensorValue(float64(i)),
		}); err != nil {
			t.Fatal(err)
		}
	}
	s := newTestServer(t, store, &fakePoll{})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/devices/dev-1/sensors?limit=1000", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	// One header <tr> plus one per sensor row.
	want := maxListLimitForTest + 1
	if got := strings.Count(rr.Body.String(), "<tr>"); got != want {
		t.Fatalf("expected %d <tr> (clamped to %d rows + header), got %d", want, maxListLimitForTest, got)
	}
}

// maxListLimitForTest mirrors internal/ui's unexported maxListLimit (200).
const maxListLimitForTest = 200
