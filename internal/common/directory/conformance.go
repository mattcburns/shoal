package directory

import (
	"context"
	"errors"
	"testing"

	"github.com/mattcburns/shoal/internal/common/models"
)

// RunConformance exercises the documented Store contract against a fresh
// Store returned by newStore. Call newStore() to obtain an independent,
// empty Store for RunConformance's exclusive use -- it must not share state
// with any other Store instance (e.g. a t.TempDir()-backed FileStore, or a
// freshly constructed NetBox adapter against a scratch/mock backend).
//
// This file intentionally has no _test.go suffix and no build tag, even
// though it imports "testing" and takes a *testing.T: it is meant to be
// imported and called from other packages' test files (this package's own
// store_test.go, and internal/common/netbox's tests for its Store
// adapter), which only works for a file compiled into the normal (non-test)
// build of the importing package's dependency graph.
//
// RunConformance calls t.Fatalf/t.Errorf on any deviation from the
// contract documented on the Store interface and FileStore.
func RunConformance(t *testing.T, newStore func() Store) {
	t.Helper()
	ctx := context.Background()

	t.Run("empty store", func(t *testing.T) {
		s := newStore()
		got, err := s.ListDevices(ctx)
		if err != nil {
			t.Fatalf("ListDevices on empty store: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("ListDevices on empty store = %v, want empty", got)
		}
	})

	t.Run("upsert then get round-trip", func(t *testing.T) {
		s := newStore()
		in := models.DeviceIdentity{
			Name:           "node-1",
			Serial:         "SN-100",
			Vendor:         "Acme",
			Model:          "R1",
			LifecycleState: models.StateDiscovered,
			CredentialRef:  "cred-1",
			BMCIP:          "10.0.0.5",
		}
		id, err := s.UpsertDevice(ctx, in)
		if err != nil {
			t.Fatalf("UpsertDevice: %v", err)
		}
		if id == "" {
			t.Fatalf("UpsertDevice returned empty id")
		}
		got, err := s.GetDevice(ctx, id)
		if err != nil {
			t.Fatalf("GetDevice(%q): %v", id, err)
		}
		if got.ID != id || got.Name != in.Name || got.Serial != in.Serial ||
			got.Vendor != in.Vendor || got.Model != in.Model ||
			got.LifecycleState != in.LifecycleState ||
			got.CredentialRef != in.CredentialRef || got.BMCIP != in.BMCIP {
			t.Fatalf("GetDevice round-trip = %+v, want fields matching %+v (id=%s)", got, in, id)
		}
	})

	t.Run("repeated upsert updates not duplicates", func(t *testing.T) {
		s := newStore()
		in := models.DeviceIdentity{
			Serial:         "SN-200",
			Name:           "node-2",
			LifecycleState: models.StateDiscovered,
		}
		id1, err := s.UpsertDevice(ctx, in)
		if err != nil {
			t.Fatalf("first UpsertDevice: %v", err)
		}
		in2 := in
		in2.Vendor = "NewVendor"
		id2, err := s.UpsertDevice(ctx, in2)
		if err != nil {
			t.Fatalf("second UpsertDevice: %v", err)
		}
		if id1 != id2 {
			t.Fatalf("second UpsertDevice with same serial returned different id: %q vs %q", id1, id2)
		}
		list, err := s.ListDevices(ctx)
		if err != nil {
			t.Fatalf("ListDevices: %v", err)
		}
		if len(list) != 1 {
			t.Fatalf("ListDevices after repeated upsert = %d devices, want 1: %+v", len(list), list)
		}
		if list[0].Vendor != "NewVendor" {
			t.Fatalf("ListDevices[0].Vendor = %q, want updated value %q", list[0].Vendor, "NewVendor")
		}
	})

	t.Run("list reflects upsert", func(t *testing.T) {
		s := newStore()
		if _, err := s.UpsertDevice(ctx, models.DeviceIdentity{Serial: "SN-300", Name: "node-3"}); err != nil {
			t.Fatalf("UpsertDevice: %v", err)
		}
		if _, err := s.UpsertDevice(ctx, models.DeviceIdentity{Serial: "SN-301", Name: "node-4"}); err != nil {
			t.Fatalf("UpsertDevice: %v", err)
		}
		list, err := s.ListDevices(ctx)
		if err != nil {
			t.Fatalf("ListDevices: %v", err)
		}
		if len(list) != 2 {
			t.Fatalf("ListDevices = %d devices, want 2: %+v", len(list), list)
		}
	})

	t.Run("set lifecycle then get shows new state", func(t *testing.T) {
		s := newStore()
		id, err := s.UpsertDevice(ctx, models.DeviceIdentity{
			Serial:         "SN-400",
			Name:           "node-5",
			LifecycleState: models.StateDiscovered,
		})
		if err != nil {
			t.Fatalf("UpsertDevice: %v", err)
		}
		if err := s.SetLifecycle(ctx, id, models.StateReady); err != nil {
			t.Fatalf("SetLifecycle by id: %v", err)
		}
		got, err := s.GetDevice(ctx, id)
		if err != nil {
			t.Fatalf("GetDevice: %v", err)
		}
		if got.LifecycleState != models.StateReady {
			t.Fatalf("LifecycleState after SetLifecycle = %q, want %q", got.LifecycleState, models.StateReady)
		}
		// SetLifecycle by serial should also resolve.
		if err := s.SetLifecycle(ctx, "SN-400", models.StateProvisioning); err != nil {
			t.Fatalf("SetLifecycle by serial: %v", err)
		}
		got, err = s.GetDevice(ctx, id)
		if err != nil {
			t.Fatalf("GetDevice: %v", err)
		}
		if got.LifecycleState != models.StateProvisioning {
			t.Fatalf("LifecycleState after SetLifecycle by serial = %q, want %q", got.LifecycleState, models.StateProvisioning)
		}
	})

	t.Run("resolve device id by serial name and id", func(t *testing.T) {
		s := newStore()
		id, err := s.UpsertDevice(ctx, models.DeviceIdentity{
			Serial: "SN-500",
			Name:   "node-6",
		})
		if err != nil {
			t.Fatalf("UpsertDevice: %v", err)
		}
		if got, err := s.ResolveDeviceID(ctx, "SN-500"); err != nil || got != id {
			t.Fatalf("ResolveDeviceID by serial = (%q, %v), want (%q, nil)", got, err, id)
		}
		if got, err := s.ResolveDeviceID(ctx, "node-6"); err != nil || got != id {
			t.Fatalf("ResolveDeviceID by name = (%q, %v), want (%q, nil)", got, err, id)
		}
		if got, err := s.ResolveDeviceID(ctx, id); err != nil || got != id {
			t.Fatalf("ResolveDeviceID by id = (%q, %v), want (%q, nil)", got, err, id)
		}
	})

	t.Run("not found errors", func(t *testing.T) {
		s := newStore()
		if _, err := s.GetDevice(ctx, "does-not-exist"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetDevice(nonexistent) err = %v, want errors.Is ErrNotFound", err)
		}
		if _, err := s.ResolveDeviceID(ctx, "does-not-exist"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("ResolveDeviceID(nonexistent) err = %v, want errors.Is ErrNotFound", err)
		}
		if err := s.DeleteDevice(ctx, "does-not-exist"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("DeleteDevice(nonexistent) err = %v, want errors.Is ErrNotFound", err)
		}
	})

	t.Run("delete then get and list confirm removal", func(t *testing.T) {
		s := newStore()
		id, err := s.UpsertDevice(ctx, models.DeviceIdentity{Serial: "SN-600", Name: "node-7"})
		if err != nil {
			t.Fatalf("UpsertDevice: %v", err)
		}
		if err := s.DeleteDevice(ctx, id); err != nil {
			t.Fatalf("DeleteDevice: %v", err)
		}
		if _, err := s.GetDevice(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetDevice after delete err = %v, want errors.Is ErrNotFound", err)
		}
		list, err := s.ListDevices(ctx)
		if err != nil {
			t.Fatalf("ListDevices after delete: %v", err)
		}
		for _, d := range list {
			if d.ID == id {
				t.Fatalf("ListDevices after delete still contains %q: %+v", id, list)
			}
		}
	})
}
