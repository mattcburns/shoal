package jobstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
)

func TestMemoryStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	s := jobstore.NewMemory()
	job := models.Job{
		ID:       "j1",
		DeviceID: "d1",
		State:    models.StateProvisioning,
	}
	if err := s.Insert(ctx, job); err != nil {
		t.Fatal(err)
	}
	p := 25
	if err := s.UpdateProgress(ctx, "j1", "IMAGE_WRITE", &p, 5, ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "j1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != "IMAGE_WRITE" || got.LastMarkerSeq != 5 || got.Percent == nil || *got.Percent != 25 {
		t.Fatalf("progress: %+v", got)
	}
	if err := s.Transition(ctx, "j1", models.StateProvisioned, ""); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, "j1")
	if got.State != models.StateProvisioned {
		t.Fatalf("state %s", got.State)
	}
	list, err := s.ListByState(ctx, models.StateProvisioned)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %d", err, len(list))
	}
	_, err = s.Get(ctx, "missing")
	if !errors.Is(err, jobstore.ErrNotFound) {
		t.Fatalf("want not found: %v", err)
	}
}

func TestMemoryListByDevice(t *testing.T) {
	ctx := context.Background()
	s := jobstore.NewMemory()
	older := time.Now().UTC().Add(-time.Hour)
	newer := time.Now().UTC()
	seed := []models.Job{
		{ID: "d4-old", DeviceID: "d4", State: models.StateProvisioned, UpdatedAt: &older},
		{ID: "d4-new", DeviceID: "d4", State: models.StateFailed, UpdatedAt: &newer},
		{ID: "other", DeviceID: "d5", State: models.StateProvisioned, UpdatedAt: &newer},
	}
	for _, j := range seed {
		if err := s.Insert(ctx, j); err != nil {
			t.Fatal(err)
		}
	}

	list, err := s.ListByDevice(ctx, "d4", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "d4-new" || list[1].ID != "d4-old" {
		t.Fatalf("want [d4-new d4-old] newest-first, got %+v", list)
	}

	filtered, err := s.ListByDevice(ctx, "d4", models.StateFailed, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].ID != "d4-new" {
		t.Fatalf("state filter: %+v", filtered)
	}

	capped, err := s.ListByDevice(ctx, "d4", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != 1 {
		t.Fatalf("limit: got %d", len(capped))
	}

	none, err := s.ListByDevice(ctx, "unknown-device", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("unknown device: %+v", none)
	}
}

func TestMemoryUpdateRuntime(t *testing.T) {
	ctx := context.Background()
	s := jobstore.NewMemory()
	if err := s.Insert(ctx, models.Job{
		ID: "j2", DeviceID: "d2", State: models.StateProvisioning, CredentialRef: "job-j2",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRuntime(ctx, "j2", "sys-1", "sol-j2", "job-j2"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "j2")
	if err != nil {
		t.Fatal(err)
	}
	if got.SystemID != "sys-1" || got.SOLSessionID != "sol-j2" || got.CredentialRef != "job-j2" {
		t.Fatalf("%+v", got)
	}
}

func TestMemoryUpdateStages(t *testing.T) {
	ctx := context.Background()
	s := jobstore.NewMemory()
	if err := s.Insert(ctx, models.Job{
		ID: "j3", DeviceID: "d3", State: models.StateProvisioning,
		Stages: []models.JobStage{{ID: "os_install", Kind: "os_install", State: "pending"}},
	}); err != nil {
		t.Fatal(err)
	}
	stages := []models.JobStage{{
		ID: "os_install", Kind: "os_install", Strategy: "image_write",
		State: "running", Phase: "WAITING_SOL", MediaURL: "http://x/y.iso",
	}}
	if err := s.UpdateStages(ctx, "j3", "os_install", "image_write", stages); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "j3")
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentStage != "os_install" || got.InstallStrategy != "image_write" {
		t.Fatalf("%+v", got)
	}
	if len(got.Stages) != 1 || got.Stages[0].State != "running" || got.Stages[0].Phase != "WAITING_SOL" {
		t.Fatalf("stages %+v", got.Stages)
	}
}
