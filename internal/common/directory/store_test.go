package directory

import (
	"context"
	"errors"
	"testing"

	"github.com/mattcburns/shoal/internal/common/models"
)

func TestFileStoreCRUD(t *testing.T) {
	ctx := context.Background()
	st, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	devices, err := st.ListDevices(ctx)
	if err != nil {
		t.Fatalf("ListDevices empty: %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("expected empty list, got %d", len(devices))
	}

	id, err := st.UpsertDevice(ctx, models.DeviceIdentity{
		Name:   "lab-node-1",
		Serial: "SN1",
		BMCIP:  "10.0.0.5",
	})
	if err != nil {
		t.Fatalf("UpsertDevice create: %v", err)
	}
	if id == "" {
		t.Fatal("expected a generated ID")
	}

	got, err := st.GetDevice(ctx, id)
	if err != nil {
		t.Fatalf("GetDevice: %v", err)
	}
	if got.Name != "lab-node-1" || got.Serial != "SN1" {
		t.Fatalf("unexpected device: %+v", got)
	}

	// Resolve by name and by serial, not just canonical ID.
	if got2, err := st.ResolveDeviceID(ctx, "lab-node-1"); err != nil || got2 != id {
		t.Fatalf("ResolveDeviceID by name = %q, %v; want %q, nil", got2, err, id)
	}
	if got2, err := st.ResolveDeviceID(ctx, "SN1"); err != nil || got2 != id {
		t.Fatalf("ResolveDeviceID by serial = %q, %v; want %q, nil", got2, err, id)
	}
	if _, err := st.ResolveDeviceID(ctx, "does-not-exist"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ResolveDeviceID unknown key: got %v, want ErrNotFound", err)
	}

	// Update preserves ID.
	got.Vendor = "Dell"
	if updatedID, err := st.UpsertDevice(ctx, got); err != nil || updatedID != id {
		t.Fatalf("UpsertDevice update: id=%q err=%v", updatedID, err)
	}
	got, err = st.GetDevice(ctx, id)
	if err != nil || got.Vendor != "Dell" {
		t.Fatalf("update did not persist: %+v, %v", got, err)
	}

	if err := st.SetLifecycle(ctx, "lab-node-1", models.StateReady); err != nil {
		t.Fatalf("SetLifecycle: %v", err)
	}
	got, err = st.GetDevice(ctx, id)
	if err != nil || got.LifecycleState != models.StateReady {
		t.Fatalf("SetLifecycle did not persist: %+v, %v", got, err)
	}

	devices, err = st.ListDevices(ctx)
	if err != nil || len(devices) != 1 {
		t.Fatalf("ListDevices after upsert: %v, %v", devices, err)
	}

	if err := st.DeleteDevice(ctx, id); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	if _, err := st.GetDevice(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetDevice after delete: got %v, want ErrNotFound", err)
	}
	if err := st.DeleteDevice(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteDevice twice: got %v, want ErrNotFound", err)
	}
}

func TestGetDeviceNotFound(t *testing.T) {
	st, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if _, err := st.GetDevice(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}
