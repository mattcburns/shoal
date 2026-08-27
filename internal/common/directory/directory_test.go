package directory_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mattcburns/shoal/internal/common/directory"
	"github.com/mattcburns/shoal/internal/common/models"
)

func testStores(t *testing.T) map[string]directory.Store {
	t.Helper()
	fs, err := directory.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return map[string]directory.Store{
		"FileStore": fs,
		"Memory":    directory.NewMemory(),
	}
}

func TestUpsertAndGetDevice(t *testing.T) {
	for name, store := range testStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			id, err := store.UpsertDevice(ctx, models.DeviceIdentity{
				Name: "lab-node-1", Serial: "SN123", BMCIP: "10.0.0.5",
			})
			if err != nil {
				t.Fatal(err)
			}
			if id == "" {
				t.Fatal("want non-empty id")
			}
			got, err := store.GetDevice(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if got.Name != "lab-node-1" || got.Serial != "SN123" || got.BMCIP != "10.0.0.5" {
				t.Fatalf("got %+v", got)
			}
			if got.LifecycleState != models.StateDiscovered {
				t.Fatalf("want default lifecycle_state discovered, got %q", got.LifecycleState)
			}
		})
	}
}

func TestGetDeviceNotFound(t *testing.T) {
	for name, store := range testStores(t) {
		t.Run(name, func(t *testing.T) {
			_, err := store.GetDevice(context.Background(), "does-not-exist")
			if !errors.Is(err, directory.ErrNotFound) {
				t.Fatalf("want ErrNotFound, got %v", err)
			}
		})
	}
}

func TestListDevicesSortedByID(t *testing.T) {
	for name, store := range testStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if _, err := store.UpsertDevice(ctx, models.DeviceIdentity{ID: "b", Name: "b-node"}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.UpsertDevice(ctx, models.DeviceIdentity{ID: "a", Name: "a-node"}); err != nil {
				t.Fatal(err)
			}
			list, err := store.ListDevices(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(list) != 2 || list[0].ID != "a" || list[1].ID != "b" {
				t.Fatalf("want sorted [a b], got %+v", list)
			}
		})
	}
}

func TestResolveDeviceIDBySerialOrName(t *testing.T) {
	for name, store := range testStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			id, err := store.UpsertDevice(ctx, models.DeviceIdentity{Name: "lab-node-1", Serial: "SN123"})
			if err != nil {
				t.Fatal(err)
			}
			gotBySerial, err := store.ResolveDeviceID(ctx, "SN123")
			if err != nil || gotBySerial != id {
				t.Fatalf("resolve by serial: id=%q err=%v", gotBySerial, err)
			}
			gotByName, err := store.ResolveDeviceID(ctx, "lab-node-1")
			if err != nil || gotByName != id {
				t.Fatalf("resolve by name: id=%q err=%v", gotByName, err)
			}
			gotByID, err := store.ResolveDeviceID(ctx, id)
			if err != nil || gotByID != id {
				t.Fatalf("resolve by id: id=%q err=%v", gotByID, err)
			}
			if _, err := store.ResolveDeviceID(ctx, "nope"); !errors.Is(err, directory.ErrNotFound) {
				t.Fatalf("want ErrNotFound, got %v", err)
			}
		})
	}
}

func TestSetLifecycle(t *testing.T) {
	for name, store := range testStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			id, err := store.UpsertDevice(ctx, models.DeviceIdentity{Name: "lab-node-1", Serial: "SN123"})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetLifecycle(ctx, "SN123", models.StateProvisioned); err != nil {
				t.Fatal(err)
			}
			got, err := store.GetDevice(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if got.LifecycleState != models.StateProvisioned {
				t.Fatalf("want provisioned, got %q", got.LifecycleState)
			}
		})
	}
}

func TestDeleteDevice(t *testing.T) {
	for name, store := range testStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			id, err := store.UpsertDevice(ctx, models.DeviceIdentity{Name: "lab-node-1"})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.DeleteDevice(ctx, id); err != nil {
				t.Fatal(err)
			}
			if _, err := store.GetDevice(ctx, id); !errors.Is(err, directory.ErrNotFound) {
				t.Fatalf("want ErrNotFound after delete, got %v", err)
			}
			if err := store.DeleteDevice(ctx, id); !errors.Is(err, directory.ErrNotFound) {
				t.Fatalf("want ErrNotFound deleting again, got %v", err)
			}
		})
	}
}
