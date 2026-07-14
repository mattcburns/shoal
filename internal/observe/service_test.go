package observe_test

import (
	"context"
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/telemetry"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
	"github.com/mattcburns/shoal/internal/observe"
)

type fakeWatch struct{ ids map[string]bool }

func (f fakeWatch) HasWatch(id string) bool { return f.ids[id] }

func TestStatusAggregatesJobAndEvents(t *testing.T) {
	jobs := jobstore.NewMemory()
	store := telemetry.NewMemory()
	ctx := context.Background()
	now := time.Now().UTC()
	pct := 40
	_ = jobs.Insert(ctx, models.ProvisioningJob{
		ID: "j1", DeviceID: "node-1", State: models.StateProvisioning,
		Phase: "IMAGE_WRITE", Percent: &pct, UpdatedAt: &now,
	})
	_ = store.WriteEvent(ctx, models.NormalizedEvent{
		DeviceID: "node-1", Message: "fan degraded", Severity: "warning", Timestamp: now,
	})

	svc := observe.New(nil, jobs, store, fakeWatch{ids: map[string]bool{"node-1": true}})
	st, err := svc.Status(ctx, "node-1")
	if err != nil {
		t.Fatal(err)
	}
	if st.ActiveJobID != "j1" || st.Phase != "IMAGE_WRITE" {
		t.Fatalf("%+v", st)
	}
	if st.LifecycleState != models.StateProvisioning {
		t.Fatalf("state=%s", st.LifecycleState)
	}
	if st.LastEvent == "" || st.Percent == nil || *st.Percent != 40 {
		t.Fatalf("%+v", st)
	}
}

func TestStatusEmptyDevice(t *testing.T) {
	svc := observe.New(nil, jobstore.NewMemory(), telemetry.NewMemory(), nil)
	_, err := svc.Status(context.Background(), "")
	if err == nil {
		t.Fatal("expected error")
	}
}
