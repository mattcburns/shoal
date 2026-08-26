package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/api"
	"github.com/mattcburns/shoal/internal/common/config"
	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/telemetry"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
	"github.com/mattcburns/shoal/internal/observe"
)

// errBackendDetail is a stand-in for a raw upstream error that must never
// reach an API client verbatim (it stands in for things like a DSN, a BMC
// hostname, or a secrets-backend path).
var errBackendDetail = errors.New("dial tcp 10.9.8.7:5432: connection refused")

// erroringTelemetry wraps a telemetry.Store and forces specific List* calls
// to fail, so handlers can be tested against a "configured backend
// dependency failed" error rather than a "not configured" one.
type erroringTelemetry struct {
	telemetry.Store
	events   error
	sensors  error
	firmware error
	jobLog   error
}

func (e erroringTelemetry) ListEvents(ctx context.Context, deviceID string, since time.Time, limit int) ([]models.NormalizedEvent, error) {
	if e.events != nil {
		return nil, e.events
	}
	return e.Store.ListEvents(ctx, deviceID, since, limit)
}

func (e erroringTelemetry) ListSensors(ctx context.Context, deviceID string, since time.Time, limit int) ([]telemetry.SensorReading, error) {
	if e.sensors != nil {
		return nil, e.sensors
	}
	return e.Store.ListSensors(ctx, deviceID, since, limit)
}

func (e erroringTelemetry) ListFirmware(ctx context.Context, deviceID string, limit int) ([]telemetry.FirmwareComponent, error) {
	if e.firmware != nil {
		return nil, e.firmware
	}
	return e.Store.ListFirmware(ctx, deviceID, limit)
}

func (e erroringTelemetry) ListJobLog(ctx context.Context, jobID string, since time.Time, limit int) ([]telemetry.JobLogLine, error) {
	if e.jobLog != nil {
		return nil, e.jobLog
	}
	return e.Store.ListJobLog(ctx, jobID, since, limit)
}

// erroringJobStore wraps a jobstore.Store and forces ListByDevice to fail.
type erroringJobStore struct {
	jobstore.Store
	err error
}

func (e erroringJobStore) ListByDevice(ctx context.Context, deviceID string, state models.LifecycleState, limit int) ([]models.Job, error) {
	return nil, e.err
}

func TestDeviceStatusAndEvents(t *testing.T) {
	jobs := jobstore.NewMemory()
	store := telemetry.NewMemory()
	ctx := context.Background()
	now := time.Now().UTC()
	_ = jobs.Insert(ctx, models.Job{
		ID: "j1", DeviceID: "n1", State: models.StateProvisioning, Phase: "BOOT", UpdatedAt: &now,
	})
	_ = store.WriteEvent(ctx, models.NormalizedEvent{
		DeviceID: "n1", Message: "hello", Severity: "info", Timestamp: now,
	})
	obs := observe.New(nil, jobs, store, nil)
	s := api.New(config.Config{}, nil).WithObserve(obs)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/devices/n1/status", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	var st models.DeviceStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.ActiveJobID != "j1" {
		t.Fatalf("%+v", st)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/devices/n1/events?limit=5", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("events %d %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["events"] == nil {
		t.Fatal("events must be [] not null")
	}
}

func TestDeviceEventsLimitClamped(t *testing.T) {
	store := telemetry.NewMemory()
	ctx := context.Background()
	// maxListLimit in internal/api/server.go is 200; write more than that and
	// request an oversized ?limit= to confirm the handler clamps it rather
	// than passing the caller-supplied value straight through.
	const maxListLimit = 200
	for i := 0; i < maxListLimit+10; i++ {
		_ = store.WriteEvent(ctx, models.NormalizedEvent{
			DeviceID: "n1", Message: "hello", Severity: "info",
			Timestamp: time.Now().UTC().Add(time.Duration(i) * time.Millisecond),
		})
	}
	obs := observe.New(nil, jobstore.NewMemory(), store, nil)
	s := api.New(config.Config{}, nil).WithObserve(obs)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/devices/n1/events?limit=99999", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Events []models.NormalizedEvent `json:"events"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Events) != maxListLimit {
		t.Fatalf("want clamped to %d events, got %d", maxListLimit, len(body.Events))
	}
}

func TestDeviceEventsWithoutTelemetry(t *testing.T) {
	obs := observe.New(nil, jobstore.NewMemory(), nil, nil)
	s := api.New(config.Config{}, nil).WithObserve(obs)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/devices/x/events", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestDeviceJobs(t *testing.T) {
	jobs := jobstore.NewMemory()
	ctx := context.Background()
	older := time.Now().UTC().Add(-time.Hour)
	newer := time.Now().UTC()
	_ = jobs.Insert(ctx, models.Job{
		ID: "j-old", DeviceID: "n2", State: models.StateProvisioned, UpdatedAt: &older,
	})
	_ = jobs.Insert(ctx, models.Job{
		ID: "j-new", DeviceID: "n2", State: models.StateFailed, UpdatedAt: &newer,
	})
	_ = jobs.Insert(ctx, models.Job{
		ID: "j-other", DeviceID: "other", State: models.StateProvisioned, UpdatedAt: &newer,
	})
	s := api.New(config.Config{}, nil).WithJobStore(jobs)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/devices/n2/jobs", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		DeviceID string       `json:"device_id"`
		Jobs     []models.Job `json:"jobs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Jobs) != 2 || body.Jobs[0].ID != "j-new" {
		t.Fatalf("want [j-new j-old] newest-first, got %+v", body.Jobs)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/devices/n2/jobs?state=failed", nil)
	s.Handler().ServeHTTP(rr, req)
	body.Jobs = nil
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Jobs) != 1 || body.Jobs[0].ID != "j-new" {
		t.Fatalf("state filter: %+v", body.Jobs)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/devices/unknown/jobs", nil)
	s.Handler().ServeHTTP(rr, req)
	var empty map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &empty); err != nil {
		t.Fatal(err)
	}
	if empty["jobs"] == nil {
		t.Fatal("jobs must be [] not null")
	}
}

func TestDeviceJobsWithoutJobStore(t *testing.T) {
	s := api.New(config.Config{}, nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/devices/x/jobs", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestDeviceSensors(t *testing.T) {
	store := telemetry.NewMemory()
	ctx := context.Background()
	ts := time.Now().UTC()
	_ = store.WriteSensor(ctx, telemetry.SensorReading{
		DeviceID: "n3", TS: ts, Sensor: "Inlet Temp", Value: telemetry.SensorValue(24.5), Unit: "Cel",
	})
	obs := observe.New(nil, jobstore.NewMemory(), store, nil)
	s := api.New(config.Config{}, nil).WithObserve(obs)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/devices/n3/sensors", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	readings, ok := body["sensors"].([]any)
	if !ok || len(readings) != 1 {
		t.Fatalf("sensors: %+v", body["sensors"])
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/devices/unknown/sensors", nil)
	s.Handler().ServeHTTP(rr, req)
	body = nil
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["sensors"] == nil {
		t.Fatal("sensors must be [] not null")
	}
}

func TestDeviceSensorsWithoutTelemetry(t *testing.T) {
	obs := observe.New(nil, jobstore.NewMemory(), nil, nil)
	s := api.New(config.Config{}, nil).WithObserve(obs)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/devices/x/sensors", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestDeviceFirmwareAndPolledPower(t *testing.T) {
	store := telemetry.NewMemory()
	ctx := context.Background()
	ts := time.Now().UTC()
	_ = store.WriteFirmware(ctx, telemetry.FirmwareComponent{
		DeviceID: "n4", TS: ts, ID: "Installed-0-BIOS", Name: "BIOS", Version: "1.8.0",
	})
	_ = store.WritePower(ctx, telemetry.PowerReading{DeviceID: "n4", TS: ts, PowerState: "Off"})
	obs := observe.New(nil, jobstore.NewMemory(), store, nil)
	s := api.New(config.Config{}, nil).WithObserve(obs)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/devices/n4/firmware", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	comps, ok := body["firmware"].([]any)
	if !ok || len(comps) != 1 {
		t.Fatalf("firmware: %+v", body["firmware"])
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/devices/n4/status", nil)
	s.Handler().ServeHTTP(rr, req)
	var st models.DeviceStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.PowerState != "Off" {
		t.Fatalf("power %+v", st)
	}
}

// assertUpstreamError checks the consistent 502 response used for "a
// configured backend dependency failed": a fixed status code and a body
// that never contains the raw underlying error text.
func assertUpstreamError(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "10.9.8.7") {
		t.Fatalf("response leaked raw internal error detail: %s", rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["error"]; !ok {
		t.Fatalf("missing error field: %s", rr.Body.String())
	}
}

func TestDeviceStatusUpstreamError(t *testing.T) {
	store := erroringTelemetry{Store: telemetry.NewMemory(), events: errBackendDetail}
	obs := observe.New(nil, jobstore.NewMemory(), store, nil)
	s := api.New(config.Config{}, nil).WithObserve(obs)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/devices/n5/status", nil)
	s.Handler().ServeHTTP(rr, req)
	assertUpstreamError(t, rr)
}

func TestDeviceEventsUpstreamError(t *testing.T) {
	store := erroringTelemetry{Store: telemetry.NewMemory(), events: errBackendDetail}
	obs := observe.New(nil, jobstore.NewMemory(), store, nil)
	s := api.New(config.Config{}, nil).WithObserve(obs)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/devices/n5/events", nil)
	s.Handler().ServeHTTP(rr, req)
	assertUpstreamError(t, rr)
}

func TestDeviceJobsUpstreamError(t *testing.T) {
	jobs := erroringJobStore{Store: jobstore.NewMemory(), err: errBackendDetail}
	s := api.New(config.Config{}, nil).WithJobStore(jobs)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/devices/n5/jobs", nil)
	s.Handler().ServeHTTP(rr, req)
	assertUpstreamError(t, rr)
}

func TestDeviceSensorsUpstreamError(t *testing.T) {
	store := erroringTelemetry{Store: telemetry.NewMemory(), sensors: errBackendDetail}
	obs := observe.New(nil, jobstore.NewMemory(), store, nil)
	s := api.New(config.Config{}, nil).WithObserve(obs)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/devices/n5/sensors", nil)
	s.Handler().ServeHTTP(rr, req)
	assertUpstreamError(t, rr)
}

func TestDeviceFirmwareUpstreamError(t *testing.T) {
	store := erroringTelemetry{Store: telemetry.NewMemory(), firmware: errBackendDetail}
	obs := observe.New(nil, jobstore.NewMemory(), store, nil)
	s := api.New(config.Config{}, nil).WithObserve(obs)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/devices/n5/firmware", nil)
	s.Handler().ServeHTTP(rr, req)
	assertUpstreamError(t, rr)
}
