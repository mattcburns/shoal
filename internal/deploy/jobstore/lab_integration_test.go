//go:build integration

package jobstore_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/telemetry"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
)

func TestLabPostgresJobStore(t *testing.T) {
	dsn := os.Getenv("SHOAL_TELEMETRY_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://shoal:shoal_password@192.168.122.100:5433/shoal_telemetry?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := telemetry.OpenAndMigrate(ctx, dsn)
	if err != nil {
		t.Fatalf("open/migrate: %v", err)
	}
	defer db.Close()

	s := jobstore.NewPostgres(db)
	id := "lab-test-" + time.Now().UTC().Format("20060102T150405")
	now := time.Now().UTC()
	job := models.Job{
		ID: id, DeviceID: "lab-node-1", State: models.StateProvisioning,
		ProfileRef: "spike", Attempt: 1, StartedAt: &now, UpdatedAt: &now,
		BMCEndpoint: "http://192.168.122.100:8001",
		ISOURL:      "http://192.168.124.1:8080/shoal-marker.iso",
	}
	if err := s.Insert(ctx, job); err != nil {
		t.Fatal(err)
	}
	p := 40
	if err := s.UpdateProgress(ctx, id, "IMAGE_WRITE", &p, 7, ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != "IMAGE_WRITE" || got.LastMarkerSeq != 7 {
		t.Fatalf("got %+v", got)
	}
	if err := s.Transition(ctx, id, models.StateFailed, "lab test cleanup"); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListByState(ctx, models.StateFailed)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, j := range list {
		if j.ID == id {
			found = true
		}
	}
	if !found {
		t.Fatal("job not in FAILED list")
	}

	byDevice, err := s.ListByDevice(ctx, "lab-node-1", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, j := range byDevice {
		if j.ID == id {
			found = true
		}
	}
	if !found {
		t.Fatal("job not in ListByDevice result")
	}
	byDeviceState, err := s.ListByDevice(ctx, "lab-node-1", models.StateFailed, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range byDeviceState {
		if j.ID == id && j.State != models.StateFailed {
			t.Fatalf("state filter leaked non-matching job: %+v", j)
		}
	}

	t.Logf("postgres jobstore OK for %s", id)
}
