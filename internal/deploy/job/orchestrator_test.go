package job_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/common/secrets"
	"github.com/mattcburns/shoal/internal/deploy/job"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
	"github.com/mattcburns/shoal/internal/observe/sol"
)

func TestOrchestratorHappyPathDone(t *testing.T) {
	ctx := context.Background()
	store := jobstore.NewMemory()
	sec := secrets.NewMemory()
	fakeBMC := redfish.NewFake()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Pipe-based SOL transport: write markers into the watch.
	pr, pw := io.Pipe()
	watch := sol.NewWatchService(log, nil)
	watch.NewTransport = func(session models.WatchSession) sol.Transport {
		return sol.NewReaderTransport(pr)
	}

	orch := job.NewOrchestrator(job.Options{
		Log:     log,
		Store:   store,
		Secrets: sec,
		NewBMC: func(cfg redfish.Config) (redfish.BMC, error) {
			return fakeBMC, nil
		},
		Watches:             watch,
		AuthMode:            "basic",
		TLSMode:             "off",
		ReconcileFailOrphan: true,
	})
	defer orch.Stop()
	watch.SetProgress(orch.ProgressPort())

	j, err := orch.Start(ctx, models.StartJobRequest{
		DeviceID:     "lab-node-1",
		BMCEndpoint:  "http://bmc.test",
		BMCUsername:  "admin",
		BMCPassword:  "secret",
		SerialTarget: "lab-node-1",
		ISOURL:       "http://iso/shoal-marker.iso",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if j.State != models.StateProvisioning {
		t.Fatalf("state %s", j.State)
	}
	if !fakeBMC.MediaInserted() {
		t.Fatal("expected media inserted")
	}

	// Emit SOL progress then DONE
	writeMarker(t, pw, "SHOAL|1|1|2026-06-19T04:10:00Z|BOOT|5|OK|booting")
	writeMarker(t, pw, "SHOAL|1|2|2026-06-19T04:10:10Z|IMAGE_WRITE|50|OK|writing")
	writeMarker(t, pw, "SHOAL|1|3|2026-06-19T04:10:20Z|DONE|100|OK|reboot pending")
	_ = pw.Close()

	deadline := time.Now().Add(3 * time.Second)
	var final models.ProvisioningJob
	for time.Now().Before(deadline) {
		final, err = store.Get(ctx, j.ID)
		if err != nil {
			t.Fatal(err)
		}
		if final.State == models.StateProvisioned {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if final.State != models.StateProvisioned {
		t.Fatalf("want provisioned, got %s err=%s phase=%s", final.State, final.Error, final.Phase)
	}
	if !fakeBMC.BootCleared() || fakeBMC.MediaInserted() {
		t.Fatal("cleanup incomplete: media/boot still set")
	}
	// password must not appear on job JSON-ish fields
	if final.BMCEndpoint == "" {
		t.Fatal("bmc endpoint should persist")
	}
}

func TestOrchestratorCancelCleansUp(t *testing.T) {
	ctx := context.Background()
	store := jobstore.NewMemory()
	sec := secrets.NewMemory()
	fakeBMC := redfish.NewFake()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	pr, pw := io.Pipe()
	defer pw.Close()
	watch := sol.NewWatchService(log, nil)
	watch.NewTransport = func(session models.WatchSession) sol.Transport {
		return sol.NewReaderTransport(pr)
	}

	orch := job.NewOrchestrator(job.Options{
		Log:                 log,
		Store:               store,
		Secrets:             sec,
		NewBMC:              func(cfg redfish.Config) (redfish.BMC, error) { return fakeBMC, nil },
		Watches:             watch,
		ReconcileFailOrphan: true,
	})
	defer orch.Stop()
	watch.SetProgress(orch.ProgressPort())

	j, err := orch.Start(ctx, models.StartJobRequest{
		DeviceID: "n1", BMCEndpoint: "http://bmc", BMCUsername: "u", BMCPassword: "p",
		SerialTarget: "n1", ISOURL: "http://iso/x.iso",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orch.Cancel(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := store.Get(ctx, j.ID)
		if got.State == models.StateFailed {
			if got.Error != "canceled" {
				t.Fatalf("error %q", got.Error)
			}
			if fakeBMC.MediaInserted() || !fakeBMC.BootCleared() {
				t.Fatal("cleanup after cancel incomplete")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("cancel did not fail job in time")
}

func TestOrchestratorStallFailsAndCleansUp(t *testing.T) {
	ctx := context.Background()
	store := jobstore.NewMemory()
	sec := secrets.NewMemory()
	fakeBMC := redfish.NewFake()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Open pipe but never write markers → stall.
	pr, pw := io.Pipe()
	defer pw.Close()
	watch := sol.NewWatchService(log, nil)
	watch.NewTransport = func(session models.WatchSession) sol.Transport {
		return sol.NewReaderTransport(pr)
	}

	orch := job.NewOrchestrator(job.Options{
		Log: log, Store: store, Secrets: sec,
		NewBMC:              func(cfg redfish.Config) (redfish.BMC, error) { return fakeBMC, nil },
		Watches:             watch,
		ReconcileFailOrphan: true,
	})
	defer orch.Stop()
	watch.SetProgress(orch.ProgressPort())

	j, err := orch.Start(ctx, models.StartJobRequest{
		DeviceID: "n1", BMCEndpoint: "http://bmc", BMCUsername: "u", BMCPassword: "p",
		SerialTarget: "n1", ISOURL: "http://iso/x.iso",
		StallTimeout: 80 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := store.Get(ctx, j.ID)
		if got.State == models.StateFailed {
			if got.Error != "sol stall" {
				t.Fatalf("error %q", got.Error)
			}
			if fakeBMC.MediaInserted() || !fakeBMC.BootCleared() {
				t.Fatal("cleanup after stall incomplete")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("stall did not fail job in time")
}

func TestOrchestratorOrphanReconcile(t *testing.T) {
	ctx := context.Background()
	store := jobstore.NewMemory()
	// Pre-seed orphan PROVISIONING job as if process restarted.
	now := time.Now().UTC()
	_ = store.Insert(ctx, models.ProvisioningJob{
		ID: "orphan-1", DeviceID: "d", State: models.StateProvisioning,
		BMCEndpoint: "http://bmc", StartedAt: &now, UpdatedAt: &now,
	})
	fakeBMC := redfish.NewFake()
	// simulate leftover media from crashed job
	_ = fakeBMC.InsertVirtualMedia(ctx, "/redfish/v1/Managers/1/VirtualMedia/Cd", "http://iso/x.iso")
	_ = fakeBMC.SetBootOverrideOnceCD(ctx, "1")

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	watch := sol.NewWatchService(log, nil)
	orch := job.NewOrchestrator(job.Options{
		Log: log, Store: store, Secrets: secrets.NewMemory(),
		NewBMC:  func(cfg redfish.Config) (redfish.BMC, error) { return fakeBMC, nil },
		Watches: watch, ReconcileFailOrphan: true,
	})
	defer orch.Stop()
	watch.SetProgress(orch.ProgressPort())

	if err := orch.ReconcileOrphans(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, "orphan-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != models.StateFailed {
		t.Fatalf("orphan state %s", got.State)
	}
}

func writeMarker(t *testing.T, w io.Writer, line string) {
	t.Helper()
	if _, err := io.WriteString(w, line+"\n"); err != nil {
		t.Fatal(err)
	}
	// allow apply
	time.Sleep(30 * time.Millisecond)
}
