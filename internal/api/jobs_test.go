package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// fakeStarter records whether StartAsync was invoked, so tests can assert a
// boundary-rejected request never reaches business logic.
type fakeStarter struct {
	called bool
	job    models.Job
	err    error
}

func (f *fakeStarter) StartAsync(_ context.Context, _ models.StartJobRequest) (models.Job, error) {
	f.called = true
	return f.job, f.err
}

func TestStartJobInvalidRequestRejectedAtBoundary(t *testing.T) {
	fs := &fakeStarter{job: models.Job{ID: "should-not-start"}}
	s := api.New(config.Config{}, nil).WithJobStarter(fs)

	body := `{"device_id":"x","bmc_endpoint":"http://bmc.example","serial_target":"n1","bmc_username":"u","bmc_password":"p","install_strategy":"bogus"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewBufferString(body))
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	if fs.called {
		t.Fatal("invalid install_strategy must be rejected at the handler boundary, without reaching the orchestrator")
	}
}

func TestStartJobMissingDeviceIDRejectedAtBoundary(t *testing.T) {
	fs := &fakeStarter{}
	s := api.New(config.Config{}, nil).WithJobStarter(fs)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewBufferString(`{}`))
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	if fs.called {
		t.Fatal("empty request must be rejected at the handler boundary")
	}
}

// TestStartJobCredentialOmittedReachesOrchestrator guards against the
// regression where moving validate.StartJobRequest to the boundary rejected
// requests that intentionally omit BMC credentials to rely on
// Orchestrator.prepareStart resolving them from a NetBox device record or
// SHOAL_BMC_* env defaults (see startJobBoundaryProbe in jobs.go). The
// handler must forward such a request to StartAsync rather than 400 it
// itself; whether it ultimately succeeds is then up to the orchestrator.
func TestStartJobCredentialOmittedReachesOrchestrator(t *testing.T) {
	fs := &fakeStarter{job: models.Job{ID: "j1"}}
	s := api.New(config.Config{}, nil).WithJobStarter(fs)

	// No bmc_username/bmc_password/credential_ref at all.
	body := `{"device_id":"x","bmc_endpoint":"http://bmc.example","serial_target":"n1","iso_url":"http://iso.example/a.iso"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewBufferString(body))
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	if !fs.called {
		t.Fatal("credential-omitted request must reach the orchestrator, which may resolve credentials itself")
	}
}

// TestStartJobHTTPSEndpointWithoutSerialTargetReachesOrchestrator guards the
// second half of the same regression: an https bmc_endpoint with no explicit
// serial_target relies on Orchestrator.applyStartBindings auto-detecting
// serial_transport=redfish_sol (which doesn't need serial_target). The
// boundary probe must not require serial_target here.
func TestStartJobHTTPSEndpointWithoutSerialTargetReachesOrchestrator(t *testing.T) {
	fs := &fakeStarter{job: models.Job{ID: "j1"}}
	s := api.New(config.Config{}, nil).WithJobStarter(fs)

	body := `{"device_id":"x","bmc_endpoint":"https://bmc.example","credential_ref":"ref1","iso_url":"http://iso.example/a.iso"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", bytes.NewBufferString(body))
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	if !fs.called {
		t.Fatal("https endpoint without serial_target must reach the orchestrator, which auto-detects redfish_sol")
	}
}

func TestGetJob(t *testing.T) {
	store := jobstore.NewMemory()
	now := time.Now().UTC()
	_ = store.Insert(context.Background(), models.Job{
		ID: "j1", DeviceID: "d1", State: models.StateProvisioning,
		UpdatedAt: &now, CurrentStage: models.JobStageKindOSInstall,
		InstallStrategy: models.InstallStrategyImageWrite,
		Stages: []models.JobStage{{
			ID: models.JobStageKindOSInstall, Kind: models.JobStageKindOSInstall,
			Strategy: models.InstallStrategyImageWrite, State: models.JobStageStateRunning,
		}},
	})
	s := api.New(config.Config{}, nil).WithJobStore(store)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/jobs/j1", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	var j models.Job
	if err := json.Unmarshal(rr.Body.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	if j.ID != "j1" {
		t.Fatalf("id %s", j.ID)
	}
	// M6 AC: GET exposes current_stage + stages
	if j.CurrentStage != models.JobStageKindOSInstall {
		t.Fatalf("current_stage=%s", j.CurrentStage)
	}
	if len(j.Stages) != 1 {
		t.Fatalf("stages=%d", len(j.Stages))
	}
}

func TestGetJobNotFound(t *testing.T) {
	s := api.New(config.Config{}, nil).WithJobStore(jobstore.NewMemory())
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/jobs/missing", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestJobLog(t *testing.T) {
	store := telemetry.NewMemory()
	ctx := context.Background()
	base := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	_ = store.WriteJobLog(ctx, "job-1", base, "first")
	_ = store.WriteJobLog(ctx, "job-1", base.Add(time.Minute), "second")
	obs := observe.New(nil, jobstore.NewMemory(), store, nil)
	s := api.New(config.Config{}, nil).WithObserve(obs)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/jobs/job-1/log", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		JobID string                 `json:"job_id"`
		Lines []telemetry.JobLogLine `json:"lines"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Lines) != 2 || body.Lines[0].Line != "first" || body.Lines[1].Line != "second" {
		t.Fatalf("want oldest-first [first second], got %+v", body.Lines)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/jobs/unknown-job/log", nil)
	s.Handler().ServeHTTP(rr, req)
	var empty map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &empty); err != nil {
		t.Fatal(err)
	}
	if empty["lines"] == nil {
		t.Fatal("lines must be [] not null")
	}
}

func TestJobLogUpstreamError(t *testing.T) {
	store := erroringTelemetry{Store: telemetry.NewMemory(), jobLog: errBackendDetail}
	obs := observe.New(nil, jobstore.NewMemory(), store, nil)
	s := api.New(config.Config{}, nil).WithObserve(obs)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/jobs/job-1/log", nil)
	s.Handler().ServeHTTP(rr, req)
	assertUpstreamError(t, rr)
}

func TestJobLogWithoutTelemetry(t *testing.T) {
	obs := observe.New(nil, jobstore.NewMemory(), nil, nil)
	s := api.New(config.Config{}, nil).WithObserve(obs)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/jobs/x/log", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", rr.Code)
	}
}

type fakeCanceler struct {
	err error
	id  string
}

func (f *fakeCanceler) Cancel(_ context.Context, jobID string) error {
	f.id = jobID
	return f.err
}

func TestCancelJob(t *testing.T) {
	store := jobstore.NewMemory()
	now := time.Now().UTC()
	_ = store.Insert(context.Background(), models.Job{
		ID: "j1", DeviceID: "d1", State: models.StateProvisioning, UpdatedAt: &now,
	})
	fc := &fakeCanceler{}
	s := api.New(config.Config{}, nil).WithJobStore(store).WithJobCanceler(fc)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs/j1/cancel", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	if fc.id != "j1" {
		t.Fatalf("cancel id %q", fc.id)
	}
}

func TestCancelJobNotConfigured(t *testing.T) {
	s := api.New(config.Config{}, nil).WithJobStore(jobstore.NewMemory())
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs/j1/cancel", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestCancelJobNotFound(t *testing.T) {
	fc := &fakeCanceler{err: jobstore.ErrNotFound}
	s := api.New(config.Config{}, nil).WithJobStore(jobstore.NewMemory()).WithJobCanceler(fc)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs/nope/cancel", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestCancelJobConflict(t *testing.T) {
	fc := &fakeCanceler{err: errors.New("job: cannot cancel job in state provisioned")}
	store := jobstore.NewMemory()
	now := time.Now().UTC()
	_ = store.Insert(context.Background(), models.Job{
		ID: "j1", DeviceID: "d1", State: models.StateProvisioned, UpdatedAt: &now,
	})
	s := api.New(config.Config{}, nil).WithJobStore(store).WithJobCanceler(fc)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs/j1/cancel", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status %d", rr.Code)
	}
}
