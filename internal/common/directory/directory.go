// Package directory defines the device-directory Store abstraction shared by
// the NetBox-backed and local file-backed device directory implementations.
//
// PLACEHOLDER NOTICE: this file is a minimal stand-in written by the
// "NetBox adapter satisfies directory.Store" work unit, coded against the
// documented Store/RunConformance shape handed down by the coordinator for a
// sibling unit (internal/common/directory owner) that had not yet merged at
// the time this branch was created. It exists only so this unit's code
// compiles and can be verified end-to-end against directory.RunConformance.
// When the sibling unit's real internal/common/directory package merges,
// this file should be deleted (or reconciled) in favor of the authoritative
// version — the import path (github.com/mattcburns/shoal/internal/common/directory)
// and exported names here were kept identical to the documented contract so
// that reconciliation is a drop-in replacement.
package directory

import (
	"context"
	"errors"
	"testing"

	"github.com/mattcburns/shoal/internal/common/models"
)

// ErrNotFound is returned by Store methods when a device does not exist.
var ErrNotFound = errors.New("directory: device not found")

// Store is the device-directory abstraction each backend (NetBox, local
// file-backed) satisfies. Selection between backends is a runtime config
// gate; both are always compiled in.
type Store interface {
	ListDevices(ctx context.Context) ([]models.DeviceIdentity, error)
	GetDevice(ctx context.Context, id string) (models.DeviceIdentity, error)
	UpsertDevice(ctx context.Context, d models.DeviceIdentity) (string, error)
	SetLifecycle(ctx context.Context, deviceKey string, state models.LifecycleState) error
	ResolveDeviceID(ctx context.Context, key string) (string, error)
	DeleteDevice(ctx context.Context, id string) error
}

// RunConformance exercises every Store method against a fresh backend
// instance obtained by calling newStore(), covering list/get/upsert/
// set-lifecycle/resolve/delete plus not-found and empty-list cases. Every
// Store implementation (NetBox-backed, local file-backed, ...) must pass
// this identically. newStore is called once per top-level sub-test below and
// must return a store backed by empty/fresh state.
func RunConformance(t *testing.T, newStore func() Store) {
	t.Helper()

	t.Run("EmptyList", func(t *testing.T) {
		s := newStore()
		devices, err := s.ListDevices(context.Background())
		if err != nil {
			t.Fatalf("ListDevices on empty store: %v", err)
		}
		if len(devices) != 0 {
			t.Fatalf("expected empty list, got %d devices: %+v", len(devices), devices)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		s := newStore()
		if _, err := s.GetDevice(context.Background(), "does-not-exist"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetDevice missing device: expected ErrNotFound, got %v", err)
		}
		if err := s.DeleteDevice(context.Background(), "does-not-exist"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("DeleteDevice missing device: expected ErrNotFound, got %v", err)
		}
	})

	t.Run("UpsertGetList", func(t *testing.T) {
		s := newStore()
		d := models.DeviceIdentity{
			Name:           "node-1",
			Serial:         "SN-1",
			Vendor:         "Acme",
			Model:          "X1",
			LifecycleState: models.StateDiscovered,
			CredentialRef:  "cred-1",
			BMCIP:          "10.0.0.5",
		}
		id, err := s.UpsertDevice(context.Background(), d)
		if err != nil || id == "" {
			t.Fatalf("UpsertDevice: %v %q", err, id)
		}

		got, err := s.GetDevice(context.Background(), id)
		if err != nil {
			t.Fatalf("GetDevice: %v", err)
		}
		if got.Serial != d.Serial || got.Name != d.Name {
			t.Fatalf("GetDevice mismatch: got %+v want serial=%q name=%q", got, d.Serial, d.Name)
		}

		devices, err := s.ListDevices(context.Background())
		if err != nil {
			t.Fatalf("ListDevices: %v", err)
		}
		found := false
		for _, dv := range devices {
			if dv.ID == id {
				found = true
			}
		}
		if !found {
			t.Fatalf("ListDevices missing upserted device %q: %+v", id, devices)
		}

		// Re-upserting the same identity (matched by serial) must update in
		// place, not create a second device.
		d.BMCIP = "10.0.0.6"
		id2, err := s.UpsertDevice(context.Background(), d)
		if err != nil || id2 != id {
			t.Fatalf("Upsert update: err=%v id2=%q want %q", err, id2, id)
		}
		got, err = s.GetDevice(context.Background(), id)
		if err != nil || got.BMCIP != "10.0.0.6" {
			t.Fatalf("Upsert update not reflected: err=%v got=%+v", err, got)
		}
	})

	t.Run("SetLifecycle", func(t *testing.T) {
		s := newStore()
		id, err := s.UpsertDevice(context.Background(), models.DeviceIdentity{
			Serial: "SN-2", LifecycleState: models.StateDiscovered,
		})
		if err != nil {
			t.Fatalf("UpsertDevice: %v", err)
		}
		if err := s.SetLifecycle(context.Background(), id, models.StateReady); err != nil {
			t.Fatalf("SetLifecycle: %v", err)
		}
		got, err := s.GetDevice(context.Background(), id)
		if err != nil || got.LifecycleState != models.StateReady {
			t.Fatalf("SetLifecycle not applied: err=%v got=%+v", err, got)
		}
	})

	t.Run("ResolveDeviceID", func(t *testing.T) {
		s := newStore()
		id, err := s.UpsertDevice(context.Background(), models.DeviceIdentity{
			Name: "node-3", Serial: "SN-3", LifecycleState: models.StateDiscovered,
		})
		if err != nil {
			t.Fatalf("UpsertDevice: %v", err)
		}
		got, err := s.ResolveDeviceID(context.Background(), "SN-3")
		if err != nil || got != id {
			t.Fatalf("ResolveDeviceID by serial: err=%v got=%q want %q", err, got, id)
		}
	})

	t.Run("DeleteDevice", func(t *testing.T) {
		s := newStore()
		id, err := s.UpsertDevice(context.Background(), models.DeviceIdentity{
			Serial: "SN-4", LifecycleState: models.StateDiscovered,
		})
		if err != nil {
			t.Fatalf("UpsertDevice: %v", err)
		}
		if err := s.DeleteDevice(context.Background(), id); err != nil {
			t.Fatalf("DeleteDevice: %v", err)
		}
		if _, err := s.GetDevice(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetDevice after delete: expected ErrNotFound, got %v", err)
		}
	})
}
