package api_test

import (
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
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
)

func TestGetJob(t *testing.T) {
	store := jobstore.NewMemory()
	now := time.Now().UTC()
	_ = store.Insert(context.Background(), models.ProvisioningJob{
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
	var j models.ProvisioningJob
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
	_ = store.Insert(context.Background(), models.ProvisioningJob{
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
	_ = store.Insert(context.Background(), models.ProvisioningJob{
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
