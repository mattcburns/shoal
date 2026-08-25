package job_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/netbox"
	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/common/secrets"
	"github.com/mattcburns/shoal/internal/common/telemetry"
	"github.com/mattcburns/shoal/internal/deploy/job"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
	"github.com/mattcburns/shoal/internal/observe/sol"
)

func TestOrchestratorHappyPathDone(t *testing.T) {
	ctx := context.Background()
	store := jobstore.NewMemory()
	sec := secrets.NewMemory()
	fakeBMC := redfish.NewFake()
	nb := netbox.NewMemory()
	_, _ = nb.UpsertDevice(ctx, models.DeviceIdentity{
		Serial: "lab-node-1", LifecycleState: models.StateDiscovered,
	})
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
		NetBox:              nb,
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
	if nb.BySerial["lab-node-1"].LifecycleState != models.StateProvisioning {
		t.Fatalf("netbox want provisioning, got %s", nb.BySerial["lab-node-1"].LifecycleState)
	}
	// Runtime coords must be durable for out-of-process cancel.
	loaded, err := store.Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SystemID == "" || loaded.CredentialRef == "" || loaded.SOLSessionID == "" {
		t.Fatalf("runtime not persisted: system=%q cred=%q sol=%q", loaded.SystemID, loaded.CredentialRef, loaded.SOLSessionID)
	}

	// Emit SOL progress then DONE
	writeMarker(t, pw, "SHOAL|1|1|2026-06-19T04:10:00Z|BOOT|5|OK|booting")
	writeMarker(t, pw, "SHOAL|1|2|2026-06-19T04:10:10Z|IMAGE_WRITE|50|OK|writing")
	writeMarker(t, pw, "SHOAL|1|3|2026-06-19T04:10:20Z|DONE|100|OK|reboot pending")
	_ = pw.Close()

	deadline := time.Now().Add(3 * time.Second)
	var final models.Job
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
	if nb.BySerial["lab-node-1"].LifecycleState != models.StateProvisioned {
		t.Fatalf("netbox want provisioned, got %s", nb.BySerial["lab-node-1"].LifecycleState)
	}
	// password must not appear on job JSON-ish fields
	if final.BMCEndpoint == "" {
		t.Fatal("bmc endpoint should persist")
	}
	// With NetBox Memory, serial lab-node-1 remaps to numeric pk for plugin tabs.
	if j.DeviceID != "1" {
		t.Fatalf("want device_id remapped to NetBox pk, got %q", j.DeviceID)
	}
}

// TestOrchestratorDeletesEphemeralCredentialOnDone proves the "job-"+ID
// credential Start mints when the caller supplies raw username/password
// (no persistent credential_ref) is deleted once the job reaches a
// terminal state -- it's never referenced again after that, and nothing
// else cleaned these up before this change (docs/deprovision-design.md
// Key Decision 6).
func TestOrchestratorDeletesEphemeralCredentialOnDone(t *testing.T) {
	ctx := context.Background()
	store := jobstore.NewMemory()
	sec := secrets.NewMemory()
	fakeBMC := redfish.NewFake()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	pr, pw := io.Pipe()
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
		DeviceID: "n1", BMCEndpoint: "http://bmc.test", BMCUsername: "admin", BMCPassword: "secret",
		SerialTarget: "n1", ISOURL: "http://iso/shoal-marker.iso",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	loaded, err := store.Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CredentialRef != "job-"+j.ID {
		t.Fatalf("expected ephemeral ref job-%s, got %q", j.ID, loaded.CredentialRef)
	}
	// Sanity: the ref actually resolves before the job ends.
	if _, err := sec.Get(ctx, loaded.CredentialRef); err != nil {
		t.Fatalf("credential should resolve mid-job: %v", err)
	}

	writeMarker(t, pw, "SHOAL|1|1|2026-06-19T04:10:00Z|BOOT|5|OK|booting")
	writeMarker(t, pw, "SHOAL|1|2|2026-06-19T04:10:20Z|DONE|100|OK|reboot pending")
	_ = pw.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := store.Get(ctx, j.ID)
		if got.State == models.StateProvisioned {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := sec.Get(ctx, loaded.CredentialRef); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("expected ephemeral credential deleted after terminal, got err=%v", err)
	}
}

// TestOrchestratorPreservesExplicitCredentialRefOnDone proves a
// caller-supplied (persistent, device-scoped in practice) credential_ref
// is left alone on job completion -- only the "job-"+ID minting
// convention is eligible for cleanup, never an explicit ref (Key
// Decision 6's exact-match safety property).
func TestOrchestratorPreservesExplicitCredentialRefOnDone(t *testing.T) {
	ctx := context.Background()
	store := jobstore.NewMemory()
	sec := secrets.NewMemory()
	fakeBMC := redfish.NewFake()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := sec.Put(ctx, "bmc-n1", secrets.Credential{Username: "admin", Password: "secret"}); err != nil {
		t.Fatal(err)
	}

	pr, pw := io.Pipe()
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
		DeviceID: "n1", BMCEndpoint: "http://bmc.test", CredentialRef: "bmc-n1",
		SerialTarget: "n1", ISOURL: "http://iso/shoal-marker.iso",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	writeMarker(t, pw, "SHOAL|1|1|2026-06-19T04:10:00Z|DONE|100|OK|reboot pending")
	_ = pw.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := store.Get(ctx, j.ID)
		if got.State == models.StateProvisioned {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := sec.Get(ctx, "bmc-n1"); err != nil {
		t.Fatalf("explicit credential_ref must survive job completion, got err=%v", err)
	}
}

// TestOrchestratorDeletesEphemeralCredentialOnFailure proves cleanup is
// unconditional on terminal reason, not gated on success.
func TestOrchestratorDeletesEphemeralCredentialOnFailure(t *testing.T) {
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
	ref := "job-" + j.ID

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := store.Get(ctx, j.ID)
		if got.State == models.StateFailed {
			if _, err := sec.Get(ctx, ref); !errors.Is(err, secrets.ErrNotFound) {
				t.Fatalf("expected ephemeral credential deleted after failure, got err=%v", err)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("stall did not fail job in time")
}

func TestStartFillsDefaultBMCCredentials(t *testing.T) {
	ctx := context.Background()
	store := jobstore.NewMemory()
	sec := secrets.NewMemory()
	fakeBMC := redfish.NewFake()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
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
		DefaultBMCUsername:  "env-admin",
		DefaultBMCPassword:  "env-secret",
		AuthMode:            "basic",
		TLSMode:             "off",
		ReconcileFailOrphan: true,
	})
	defer orch.Stop()
	watch.SetProgress(orch.ProgressPort())

	// No bmc_username/password in request — env defaults must apply.
	j, err := orch.Start(ctx, models.StartJobRequest{
		DeviceID:     "lab-1",
		BMCEndpoint:  "http://bmc.test",
		SerialTarget: "lab-1",
		ISOURL:       "http://iso/shoal-marker.iso",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// Credential should be stored under job ref with default user.
	cred, err := sec.Get(ctx, j.CredentialRef)
	if err != nil {
		t.Fatal(err)
	}
	if cred.Username != "env-admin" || cred.Password != "env-secret" {
		t.Fatalf("cred=%+v", cred)
	}
	_ = pw.Close()
	_ = orch.Cancel(ctx, j.ID)
}

func TestStartResolvesDeviceIDAndWritesJobLog(t *testing.T) {
	ctx := context.Background()
	store := jobstore.NewMemory()
	sec := secrets.NewMemory()
	fakeBMC := redfish.NewFake()
	nb := netbox.NewMemory()
	netboxID, err := nb.UpsertDevice(ctx, models.DeviceIdentity{
		Name: "shoal-node-1", Serial: "shoal-node-1", LifecycleState: models.StateDiscovered,
	})
	if err != nil {
		t.Fatal(err)
	}
	telem := telemetry.NewMemory()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
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
		NetBox:              nb,
		Telemetry:           telem,
		AuthMode:            "basic",
		TLSMode:             "off",
		ReconcileFailOrphan: true,
	})
	defer orch.Stop()
	watch.SetProgress(orch.ProgressPort())

	j, err := orch.Start(ctx, models.StartJobRequest{
		DeviceID:     "shoal-node-1", // lab hostname → NetBox pk
		BMCEndpoint:  "http://bmc.test",
		BMCUsername:  "admin",
		BMCPassword:  "secret",
		SerialTarget: "shoal-node-1",
		ISOURL:       "http://iso/shoal-marker.iso",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if j.DeviceID != netboxID {
		t.Fatalf("device_id=%q want NetBox pk %q", j.DeviceID, netboxID)
	}

	writeMarker(t, pw, "SHOAL|1|1|2026-06-19T04:10:00Z|BOOT|5|OK|booting")
	writeMarker(t, pw, "SHOAL|1|2|2026-06-19T04:10:10Z|DONE|100|OK|done")
	_ = pw.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		final, _ := store.Get(ctx, j.ID)
		if final.State == models.StateProvisioned {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	lines, err := telem.ListJobLog(ctx, j.ID, time.Time{}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) < 2 {
		t.Fatalf("want job_log lines from markers, got %d", len(lines))
	}
	if !strings.Contains(lines[0].Line, "SHOAL|") || !strings.Contains(lines[0].Line, "BOOT") {
		t.Fatalf("first line %q", lines[0].Line)
	}
}

func TestOrchestratorCancelWithoutRunStateUsesDurableRuntime(t *testing.T) {
	// Simulate out-of-process cancel: job + secrets exist, but no in-memory runState.
	ctx := context.Background()
	store := jobstore.NewMemory()
	sec := secrets.NewMemory()
	fakeBMC := redfish.NewFake()
	// Pre-insert media as if another process started the job.
	_ = fakeBMC.InsertVirtualMedia(ctx, "/redfish/v1/Managers/1/VirtualMedia/Cd", "http://iso/x.iso")
	_ = fakeBMC.SetBootOverrideOnceCD(ctx, "1")
	_ = sec.Put(ctx, "job-orphan", secrets.Credential{Username: "admin", Password: "secret"})
	now := time.Now().UTC()
	pj := models.Job{
		ID: "orphan-1", DeviceID: "d1", State: models.StateProvisioning,
		BMCEndpoint: "http://bmc.test", SystemID: "1", CredentialRef: "job-orphan",
		SOLSessionID: "sol-orphan-1", StartedAt: &now, UpdatedAt: &now,
	}
	if err := store.Insert(ctx, pj); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	watch := sol.NewWatchService(log, nil)
	orch := job.NewOrchestrator(job.Options{
		Log: log, Store: store, Secrets: sec,
		NewBMC:  func(cfg redfish.Config) (redfish.BMC, error) { return fakeBMC, nil },
		Watches: watch, AuthMode: "basic", TLSMode: "off", ReconcileFailOrphan: true,
	})
	defer orch.Stop()
	watch.SetProgress(orch.ProgressPort())

	if err := orch.Cancel(ctx, "orphan-1"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var final models.Job
	for time.Now().Before(deadline) {
		final, _ = store.Get(ctx, "orphan-1")
		if final.State == models.StateFailed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if final.State != models.StateFailed {
		t.Fatalf("state %s err=%s", final.State, final.Error)
	}
	if fakeBMC.MediaInserted() || !fakeBMC.BootCleared() {
		t.Fatal("expected cleanup using durable system_id + credential_ref")
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

// blockingCloseTransport hangs forever in Close — used to prove HandleTerminal
// still transitions after DONE (regression for happy-path hang).
type blockingCloseTransport struct {
	inner   sol.Transport
	started chan struct{}
}

func (b *blockingCloseTransport) Open(ctx context.Context, target string) (<-chan string, error) {
	return b.inner.Open(ctx, target)
}

func (b *blockingCloseTransport) Close() error {
	select {
	case <-b.started:
	default:
		close(b.started)
	}
	select {} // hang forever
}

func TestOrchestratorDoneDespiteStuckSOLClose(t *testing.T) {
	ctx := context.Background()
	store := jobstore.NewMemory()
	sec := secrets.NewMemory()
	fakeBMC := redfish.NewFake()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	pr, pw := io.Pipe()
	closeStarted := make(chan struct{})
	watch := sol.NewWatchService(log, nil)
	watch.NewTransport = func(session models.WatchSession) sol.Transport {
		return &blockingCloseTransport{
			inner:   sol.NewReaderTransport(pr),
			started: closeStarted,
		}
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
	})
	if err != nil {
		t.Fatal(err)
	}

	writeMarker(t, pw, "SHOAL|1|1|2026-06-19T04:10:00Z|BOOT|5|OK|booting")
	writeMarker(t, pw, "SHOAL|1|2|2026-06-19T04:10:20Z|DONE|100|OK|reboot pending")
	_ = pw.Close()

	// Unregister should attempt Close (which hangs); HandleTerminal must still provision.
	deadline := time.Now().Add(15 * time.Second)
	var final models.Job
	for time.Now().Before(deadline) {
		final, err = store.Get(ctx, j.ID)
		if err != nil {
			t.Fatal(err)
		}
		if final.State == models.StateProvisioned {
			select {
			case <-closeStarted:
			case <-time.After(time.Second):
				t.Fatal("expected Close to be attempted")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("want provisioned despite stuck Close, got %s phase=%s err=%s", final.State, final.Phase, final.Error)
}

func TestOrchestratorOrphanReconcile(t *testing.T) {
	ctx := context.Background()
	store := jobstore.NewMemory()
	// Pre-seed orphan PROVISIONING job as if process restarted mid-install.
	now := time.Now().UTC()
	_ = store.Insert(ctx, models.Job{
		ID: "orphan-1", DeviceID: "d", State: models.StateProvisioning,
		Phase: "WAITING_SOL", BMCEndpoint: "http://bmc",
		StartedAt: &now, UpdatedAt: &now,
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

func TestOrchestratorOrphanReconcileDoneOK(t *testing.T) {
	// Crash after SOL DONE marker was applied but before lifecycle commit.
	ctx := context.Background()
	store := jobstore.NewMemory()
	now := time.Now().UTC()
	pct := 100
	_ = store.Insert(ctx, models.Job{
		ID: "orphan-done", DeviceID: "d", State: models.StateProvisioning,
		Phase: "DONE", Percent: &pct, LastMarkerSeq: 7,
		BMCEndpoint: "http://bmc", SystemID: "1",
		StartedAt: &now, UpdatedAt: &now,
	})
	fakeBMC := redfish.NewFake()
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
	got, err := store.Get(ctx, "orphan-done")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != models.StateProvisioned {
		t.Fatalf("want provisioned for DONE orphan, got %s err=%s", got.State, got.Error)
	}
}

func TestOrchestratorScriptedISOFlatcarDualMediaCoarse(t *testing.T) {
	ctx := context.Background()
	store := jobstore.NewMemory()
	fakeBMC := redfish.NewFakeDualCD()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	watch := sol.NewWatchService(log, nil)
	watch.NewTransport = func(session models.WatchSession) sol.Transport {
		return sol.NewReaderTransport(io.NopCloser(strings.NewReader("")))
	}
	orch := job.NewOrchestrator(job.Options{
		Log: log, Store: store, Secrets: secrets.NewMemory(),
		NewBMC:  func(cfg redfish.Config) (redfish.BMC, error) { return fakeBMC, nil },
		Watches: watch, AuthMode: "basic", TLSMode: "off",
	})
	defer orch.Stop()
	watch.SetProgress(orch.ProgressPort())

	installURL := "http://iso/flatcar.iso"
	seedURL := "http://iso/ignition.iso"
	j, err := orch.Start(ctx, models.StartJobRequest{
		DeviceID:        "flatcar-1",
		BMCEndpoint:     "http://bmc.test",
		BMCUsername:     "admin",
		BMCPassword:     "secret",
		ISOURL:          installURL,
		InstallStrategy: models.InstallStrategyScriptedISO,
		OsFamily:        models.OSFamilyFlatcar,
		SeedDelivery:    models.SeedDeliverySecondMedia,
		SeedISOURL:      seedURL,
		StageTimeout:    80 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	imgs := fakeBMC.InsertedImages()
	if len(imgs) != 2 {
		t.Fatalf("want 2 inserts, got %v", imgs)
	}
	var foundInstall, foundSeed bool
	for _, u := range imgs {
		if u == installURL {
			foundInstall = true
		}
		if u == seedURL {
			foundSeed = true
		}
	}
	if !foundInstall || !foundSeed {
		t.Fatalf("expected install+ignition seed, got %v", imgs)
	}
	if j.InstallStrategy != models.InstallStrategyScriptedISO {
		t.Fatalf("strategy=%s", j.InstallStrategy)
	}

	deadline := time.Now().Add(3 * time.Second)
	var final models.Job
	for time.Now().Before(deadline) {
		final, _ = store.Get(ctx, j.ID)
		if final.State == models.StateProvisioned {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if final.State != models.StateProvisioned {
		t.Fatalf("want provisioned, got %s err=%s phase=%s", final.State, final.Error, final.Phase)
	}
	if fakeBMC.MediaInserted() {
		t.Fatal("cleanup should eject both media")
	}
}

func TestOrchestratorOperatorISOCoarseDeadline(t *testing.T) {
	ctx := context.Background()
	store := jobstore.NewMemory()
	fakeBMC := redfish.NewFake()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	watch := sol.NewWatchService(log, nil)
	// No SOL markers; coarse path must not stall-fail.
	watch.NewTransport = func(session models.WatchSession) sol.Transport {
		return sol.NewReaderTransport(io.NopCloser(strings.NewReader("")))
	}
	orch := job.NewOrchestrator(job.Options{
		Log: log, Store: store, Secrets: secrets.NewMemory(),
		NewBMC:  func(cfg redfish.Config) (redfish.BMC, error) { return fakeBMC, nil },
		Watches: watch, AuthMode: "basic", TLSMode: "off",
	})
	defer orch.Stop()
	watch.SetProgress(orch.ProgressPort())

	j, err := orch.Start(ctx, models.StartJobRequest{
		DeviceID:        "esxi-1",
		BMCEndpoint:     "http://bmc.test",
		BMCUsername:     "admin",
		BMCPassword:     "secret",
		ISOURL:          "http://iso/esxi-custom.iso",
		InstallStrategy: models.InstallStrategyOperatorISO,
		OsFamily:        models.OSFamilyESXi,
		StageTimeout:    80 * time.Millisecond,
		// no serial_target — pure coarse wait
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !fakeBMC.MediaInserted() {
		t.Fatal("expected media inserted")
	}
	if j.InstallStrategy != models.InstallStrategyOperatorISO {
		t.Fatalf("strategy=%s", j.InstallStrategy)
	}

	deadline := time.Now().Add(3 * time.Second)
	var final models.Job
	for time.Now().Before(deadline) {
		final, _ = store.Get(ctx, j.ID)
		if final.State == models.StateProvisioned {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if final.State != models.StateProvisioned {
		t.Fatalf("want provisioned, got %s err=%s phase=%s", final.State, final.Error, final.Phase)
	}
	if fakeBMC.MediaInserted() || !fakeBMC.BootCleared() {
		t.Fatal("cleanup incomplete after coarse DONE")
	}
}

func TestOrchestratorOperatorISORejectsSeed(t *testing.T) {
	ctx := context.Background()
	store := jobstore.NewMemory()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	watch := sol.NewWatchService(log, nil)
	orch := job.NewOrchestrator(job.Options{
		Log: log, Store: store, Secrets: secrets.NewMemory(),
		NewBMC:  func(cfg redfish.Config) (redfish.BMC, error) { return redfish.NewFake(), nil },
		Watches: watch, AuthMode: "basic", TLSMode: "off",
	})
	defer orch.Stop()
	watch.SetProgress(orch.ProgressPort())

	_, err := orch.Start(ctx, models.StartJobRequest{
		DeviceID:        "esxi-1",
		BMCEndpoint:     "http://bmc.test",
		BMCUsername:     "admin",
		BMCPassword:     "secret",
		ISOURL:          "http://iso/esxi.iso",
		InstallStrategy: models.InstallStrategyOperatorISO,
		OsFamily:        models.OSFamilyESXi,
		SeedDelivery:    models.SeedDeliverySecondMedia,
		SeedISOURL:      "http://iso/seed.iso",
		StageTimeout:    time.Second,
	})
	if err == nil {
		t.Fatal("expected seed with operator_iso to fail validate")
	}
}

func TestOrchestratorSecondMediaDualInsert(t *testing.T) {
	ctx := context.Background()
	store := jobstore.NewMemory()
	sec := secrets.NewMemory()
	fakeBMC := redfish.NewFakeDualCD()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pr, pw := io.Pipe()
	watch := sol.NewWatchService(log, nil)
	watch.NewTransport = func(session models.WatchSession) sol.Transport {
		return sol.NewReaderTransport(pr)
	}
	orch := job.NewOrchestrator(job.Options{
		Log: log, Store: store, Secrets: sec,
		NewBMC:  func(cfg redfish.Config) (redfish.BMC, error) { return fakeBMC, nil },
		Watches: watch, AuthMode: "basic", TLSMode: "off",
	})
	defer orch.Stop()
	watch.SetProgress(orch.ProgressPort())

	installURL := "http://iso/install.iso"
	seedURL := "http://iso/cidata.iso"
	j, err := orch.Start(ctx, models.StartJobRequest{
		DeviceID:        "lab-node-1",
		BMCEndpoint:     "http://bmc.test",
		BMCUsername:     "admin",
		BMCPassword:     "secret",
		SerialTarget:    "lab-node-1",
		ISOURL:          installURL,
		SeedDelivery:    models.SeedDeliverySecondMedia,
		SeedISOURL:      seedURL,
		InstallStrategy: models.InstallStrategySimulate,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	imgs := fakeBMC.InsertedImages()
	if len(imgs) != 2 {
		t.Fatalf("want 2 media inserts, got %v", imgs)
	}
	var foundInstall, foundSeed bool
	for _, u := range imgs {
		if u == installURL {
			foundInstall = true
		}
		if u == seedURL {
			foundSeed = true
		}
	}
	if !foundInstall || !foundSeed {
		t.Fatalf("expected install+seed URLs, got %v", imgs)
	}
	// Stage should record resolved second_media
	loaded, err := store.Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Stages) != 1 {
		t.Fatalf("stages=%d", len(loaded.Stages))
	}
	if loaded.Stages[0].SeedDelivery != models.SeedDeliverySecondMedia {
		t.Fatalf("seed_delivery=%s", loaded.Stages[0].SeedDelivery)
	}
	if loaded.Stages[0].SeedMediaURL != seedURL {
		t.Fatalf("seed_media_url=%s", loaded.Stages[0].SeedMediaURL)
	}

	writeMarker(t, pw, "SHOAL|1|1|2026-06-19T04:10:00Z|DONE|100|OK|done")
	_ = pw.Close()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		final, _ := store.Get(ctx, j.ID)
		if final.State == models.StateProvisioned {
			if fakeBMC.MediaInserted() {
				t.Fatal("cleanup should eject both media")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job did not reach provisioned")
}

func TestOrchestratorSecondMediaFailsWithOneCD(t *testing.T) {
	ctx := context.Background()
	store := jobstore.NewMemory()
	fakeBMC := redfish.NewFake() // single CD
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	watch := sol.NewWatchService(log, nil)
	watch.NewTransport = func(session models.WatchSession) sol.Transport {
		return sol.NewReaderTransport(io.NopCloser(strings.NewReader("")))
	}
	orch := job.NewOrchestrator(job.Options{
		Log: log, Store: store, Secrets: secrets.NewMemory(),
		NewBMC:  func(cfg redfish.Config) (redfish.BMC, error) { return fakeBMC, nil },
		Watches: watch, AuthMode: "basic", TLSMode: "off",
	})
	defer orch.Stop()
	watch.SetProgress(orch.ProgressPort())

	_, err := orch.Start(ctx, models.StartJobRequest{
		DeviceID:        "lab-node-1",
		BMCEndpoint:     "http://bmc.test",
		BMCUsername:     "admin",
		BMCPassword:     "secret",
		SerialTarget:    "lab-node-1",
		ISOURL:          "http://iso/install.iso",
		SeedDelivery:    models.SeedDeliverySecondMedia,
		SeedISOURL:      "http://iso/cidata.iso",
		InstallStrategy: models.InstallStrategySimulate,
	})
	if err == nil {
		t.Fatal("expected second_media with 1 CD to fail")
	}
	if !strings.Contains(err.Error(), "second_media") && !strings.Contains(err.Error(), "Virtual Media") {
		t.Fatalf("unexpected error: %v", err)
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
