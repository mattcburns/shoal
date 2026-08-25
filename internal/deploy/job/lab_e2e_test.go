//go:build integration

package job_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/common/secrets"
	"github.com/mattcburns/shoal/internal/common/telemetry"
	"github.com/mattcburns/shoal/internal/deploy/job"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
	"github.com/mattcburns/shoal/internal/observe/sol"
)

// labE2EEnv holds credentials/endpoints for live lab tests.
type labE2EEnv struct {
	base, user, pass, iso, seedISO, dsn string
	sshHost, sshUser, sshKey            string
}

func loadLabE2E(t *testing.T) labE2EEnv {
	t.Helper()
	e := labE2EEnv{
		base:    envOr("SHOAL_BMC_URL", "http://192.168.122.100:8001"),
		user:    os.Getenv("SHOAL_BMC_USERNAME"),
		pass:    os.Getenv("SHOAL_BMC_PASSWORD"),
		iso:     envOr("SHOAL_ISO_URL", "http://192.168.124.1:8080/shoal-marker.iso"),
		seedISO: envOr("SHOAL_SEED_ISO_URL", "http://192.168.124.1:8080/shoal-cidata.iso"),
		dsn:     os.Getenv("SHOAL_TELEMETRY_DATABASE_URL"),
		sshHost: os.Getenv("SHOAL_SERIAL_SSH_HOST"),
		sshUser: envOr("SHOAL_SERIAL_SSH_USER", "lab"),
		sshKey:  os.Getenv("SHOAL_SERIAL_SSH_KEY"),
	}
	if e.user == "" || e.pass == "" {
		t.Skip("SHOAL_BMC_USERNAME/PASSWORD required")
	}
	return e
}

func labStore(t *testing.T, ctx context.Context, dsn string) (jobstore.Store, func()) {
	t.Helper()
	if dsn == "" {
		return jobstore.NewMemory(), func() {}
	}
	db, err := telemetry.OpenAndMigrate(ctx, dsn)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	return jobstore.NewPostgres(db), func() { _ = db.Close() }
}

func labOrch(t *testing.T, e labE2EEnv, store jobstore.Store, watch *sol.WatchService) *job.Orchestrator {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	orch := job.NewOrchestrator(job.Options{
		Log:                 log,
		Store:               store,
		Secrets:             secrets.NewMemory(),
		NewBMC:              redfish.NewBMC,
		Watches:             watch,
		AuthMode:            "basic",
		TLSMode:             "off",
		ReconcileFailOrphan: true,
	})
	watch.SetProgress(orch.ProgressPort())
	return orch
}

func waitJobState(t *testing.T, store jobstore.Store, id string, want models.LifecycleState, d time.Duration) models.Job {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(d)
	var final models.Job
	var err error
	for time.Now().Before(deadline) {
		final, err = store.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if final.State == want {
			return final
		}
		if final.State == models.StateFailed || final.State == models.StateProvisioned {
			// terminal other than wanted
			if final.State != want {
				t.Fatalf("want %s, got %s err=%q phase=%s", want, final.State, final.Error, final.Phase)
			}
			return final
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s; last state=%s phase=%s err=%q", want, final.State, final.Phase, final.Error)
	return final
}

func mediaInserted(t *testing.T, e labE2EEnv, systemID string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	bmc, err := redfish.NewBMC(redfish.Config{BaseURL: e.base, Username: e.user, Password: e.pass, AuthMode: "basic", TLSMode: "off"})
	if err != nil {
		t.Fatal(err)
	}
	if err := bmc.Open(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bmc.Close(context.Background()) }()
	vms, err := bmc.ListVirtualMedia(ctx, systemID)
	if err != nil {
		t.Fatal(err)
	}
	for _, vm := range vms {
		if vm.Inserted {
			return true
		}
	}
	return false
}

// insertedMediaByURI returns URI -> Image for every currently-inserted
// virtual media slot on systemID (real Redfish query, not a fake).
func insertedMediaByURI(t *testing.T, e labE2EEnv, systemID string) map[string]string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	bmc, err := redfish.NewBMC(redfish.Config{BaseURL: e.base, Username: e.user, Password: e.pass, AuthMode: "basic", TLSMode: "off"})
	if err != nil {
		t.Fatal(err)
	}
	if err := bmc.Open(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bmc.Close(context.Background()) }()
	vms, err := bmc.ListVirtualMedia(ctx, systemID)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]string)
	for _, vm := range vms {
		if vm.Inserted {
			out[vm.URI] = vm.Image
		}
	}
	return out
}

// TestLabSecondMediaRejectedBySushyTools proves Shoal safely refuses
// seed_delivery=second_media against the real lab rather than silently
// misbehaving. sushy-tools' libvirt driver cannot expose two independent,
// insertable CD-typed Virtual Media slots: its InsertMedia libvirt-attach
// step (sushy_tools/emulator/resources/systems/libvirtdriver.py,
// BOOT_DEVICE_MAP) is a hardcoded Python class attribute covering exactly
// {Cd, Floppy, Hdd, Pxe}, with no config override — verified against
// upstream source, not a lab-config gap. A custom "Cd2" device (tried in an
// earlier version of this lab config via SUSHY_EMULATOR_VMEDIA_DEVICES) is
// accepted by the generic vmedia resource layer but rejected by the libvirt
// attach step with "Unknown device Cd2". second_media therefore needs real
// multi-CD-slot BMC hardware to prove end-to-end; see
// docs/lab-runbook.md. This test is the regression guard that Shoal's
// CD-counting/resolution logic (stages.go resolveSeedDelivery) correctly
// reads real sushy-tools Redfish JSON (1 CD-capable slot: "Cd"; "Floppy" is
// not CD-capable) and refuses to proceed, rather than partially attaching
// media and leaving the BMC in a half-configured state.
func TestLabSecondMediaRejectedBySushyTools(t *testing.T) {
	e := loadLabE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	store, closer := labStore(t, ctx, e.dsn)
	defer closer()

	// Injected SOL (pipe stays open so job stays PROVISIONING until cancel) —
	// same pattern as TestLabDeployCancel; unused here since Start is expected
	// to fail before any watch is ever registered.
	pr, pw := io.Pipe()
	defer pw.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	watch := sol.NewWatchService(log, nil)
	watch.NewTransport = func(models.WatchSession) sol.Transport {
		return sol.NewReaderTransport(pr)
	}
	orch := labOrch(t, e, store, watch)
	defer orch.Stop()

	// node-4: unused by the other lab_e2e tests (node-1/2/3 are claimed).
	_, err := orch.Start(ctx, models.StartJobRequest{
		DeviceID: "shoal-node-4", BMCEndpoint: e.base, BMCUsername: e.user, BMCPassword: e.pass,
		SerialTarget: "pipe", ISOURL: e.iso, SystemID: "shoal-node-4",
		SeedDelivery: models.SeedDeliverySecondMedia, SeedISOURL: e.seedISO,
		StallTimeout: 5 * time.Minute,
	})
	if err == nil {
		t.Fatal("expected second_media to be rejected against this lab (sushy-tools cannot expose 2 real CD-typed Virtual Media slots); see docs/lab-runbook.md")
	}
	if !strings.Contains(err.Error(), "CD-capable") {
		t.Fatalf("expected a CD-slot-count rejection error, got: %v", err)
	}
	// The rejection happens during stage expansion, before any BMC media
	// operation — confirm nothing was left attached.
	if media := insertedMediaByURI(t, e, "shoal-node-4"); len(media) != 0 {
		t.Fatalf("media inserted despite second_media rejection: %+v", media)
	}
	t.Logf("second_media correctly rejected against real lab: %v", err)
}

// TestLabDeployCancel starts a live job then cancels; expects FAILED + media ejected.
func TestLabDeployCancel(t *testing.T) {
	e := loadLabE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	store, closer := labStore(t, ctx, e.dsn)
	defer closer()

	// Injected SOL (pipe stays open so job stays PROVISIONING until cancel).
	pr, pw := io.Pipe()
	defer pw.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	watch := sol.NewWatchService(log, nil)
	watch.NewTransport = func(models.WatchSession) sol.Transport {
		return sol.NewReaderTransport(pr)
	}
	orch := labOrch(t, e, store, watch)
	defer orch.Stop()

	j, err := orch.Start(ctx, models.StartJobRequest{
		DeviceID: "shoal-node-2", BMCEndpoint: e.base, BMCUsername: e.user, BMCPassword: e.pass,
		SerialTarget: "pipe", ISOURL: e.iso, SystemID: "shoal-node-2",
		StallTimeout: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !mediaInserted(t, e, "shoal-node-2") {
		t.Fatal("expected media inserted before cancel")
	}
	if err := orch.Cancel(ctx, j.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	final := waitJobState(t, store, j.ID, models.StateFailed, 90*time.Second)
	if final.Error != "canceled" {
		t.Fatalf("error %q", final.Error)
	}
	if mediaInserted(t, e, "shoal-node-2") {
		t.Fatal("media still inserted after cancel cleanup")
	}
	t.Logf("cancel job %s OK", j.ID)
}

// TestLabDeployStall leaves SOL silent with a short stall timeout → FAILED + cleanup.
func TestLabDeployStall(t *testing.T) {
	e := loadLabE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	store, closer := labStore(t, ctx, e.dsn)
	defer closer()

	pr, pw := io.Pipe()
	defer pw.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	watch := sol.NewWatchService(log, nil)
	watch.NewTransport = func(models.WatchSession) sol.Transport {
		return sol.NewReaderTransport(pr)
	}
	orch := labOrch(t, e, store, watch)
	defer orch.Stop()

	// Use node-3 to avoid colliding with other tests.
	j, err := orch.Start(ctx, models.StartJobRequest{
		DeviceID: "shoal-node-3", BMCEndpoint: e.base, BMCUsername: e.user, BMCPassword: e.pass,
		SerialTarget: "pipe", ISOURL: e.iso, SystemID: "shoal-node-3",
		StallTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	final := waitJobState(t, store, j.ID, models.StateFailed, 60*time.Second)
	if final.Error != "sol stall" {
		t.Fatalf("error %q", final.Error)
	}
	if mediaInserted(t, e, "shoal-node-3") {
		t.Fatal("media still inserted after stall cleanup")
	}
	t.Logf("stall job %s OK", j.ID)
}

// TestLabDeployRealSOLSSH boots marker ISO and tails nested serial via SSH.
// Requires SHOAL_SERIAL_SSH_HOST (and usually SHOAL_SERIAL_SSH_KEY).
func TestLabDeployRealSOLSSH(t *testing.T) {
	e := loadLabE2E(t)
	if e.sshHost == "" {
		t.Skip("SHOAL_SERIAL_SSH_HOST required for real SOL path")
	}
	if e.sshKey == "" {
		if home := os.Getenv("HOME"); home != "" {
			e.sshKey = home + "/.ssh/shoal_lab_vm"
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	store, closer := labStore(t, ctx, e.dsn)
	defer closer()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	watch := sol.NewWatchService(log, nil)
	watch.NewTransport = sol.NewTransportFactory(sol.SSHSerialConfig{
		Host: e.sshHost, User: e.sshUser, KeyPath: e.sshKey, UseSudo: true,
	})
	orch := labOrch(t, e, store, watch)
	defer orch.Stop()

	// Prefer node-1 for full path (same as CLI spike).
	j, err := orch.Start(ctx, models.StartJobRequest{
		DeviceID: "shoal-node-1", BMCEndpoint: e.base, BMCUsername: e.user, BMCPassword: e.pass,
		SerialTarget: "shoal-node-1", ISOURL: e.iso, SystemID: "shoal-node-1",
		StallTimeout: 4 * time.Minute,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Logf("job %s started; waiting for SOL DONE", j.ID)
	final := waitJobState(t, store, j.ID, models.StateProvisioned, 5*time.Minute)
	if final.LastMarkerSeq < 1 || final.Phase != "DONE" {
		t.Fatalf("unexpected progress: phase=%s seq=%d", final.Phase, final.LastMarkerSeq)
	}
	if mediaInserted(t, e, "shoal-node-1") {
		t.Fatal("media still inserted after DONE cleanup")
	}
	t.Logf("real SOL job %s provisioned OK (seq=%d)", j.ID, final.LastMarkerSeq)
}
