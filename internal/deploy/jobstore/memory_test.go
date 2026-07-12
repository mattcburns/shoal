package jobstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
)

func TestMemoryStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	s := jobstore.NewMemory()
	job := models.ProvisioningJob{
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
