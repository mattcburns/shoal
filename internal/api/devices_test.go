package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/api"
	"github.com/mattcburns/shoal/internal/common/config"
	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/telemetry"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
	"github.com/mattcburns/shoal/internal/observe"
)

func TestDeviceStatusAndEvents(t *testing.T) {
	jobs := jobstore.NewMemory()
	store := telemetry.NewMemory()
	ctx := context.Background()
	now := time.Now().UTC()
	_ = jobs.Insert(ctx, models.ProvisioningJob{
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
	_ = jobs.Insert(ctx, models.ProvisioningJob{
		ID: "j-old", DeviceID: "n2", State: models.StateProvisioned, UpdatedAt: &older,
	})
	_ = jobs.Insert(ctx, models.ProvisioningJob{
		ID: "j-new", DeviceID: "n2", State: models.StateFailed, UpdatedAt: &newer,
	})
	_ = jobs.Insert(ctx, models.ProvisioningJob{
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
		DeviceID string                   `json:"device_id"`
		Jobs     []models.ProvisioningJob `json:"jobs"`
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
		DeviceID: "n3", TS: ts, Sensor: "Inlet Temp", Value: 24.5, Unit: "Cel",
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
	readings, ok := body["readings"].([]any)
	if !ok || len(readings) != 1 {
		t.Fatalf("readings: %+v", body["readings"])
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/devices/unknown/sensors", nil)
	s.Handler().ServeHTTP(rr, req)
	body = nil
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["readings"] == nil {
		t.Fatal("readings must be [] not null")
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
