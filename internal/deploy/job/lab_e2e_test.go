//go:build integration

package job_test

import (
	"context"
	"io"
	"log/slog"
	"os"
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
	base, user, pass, iso, dsn string
	sshHost, sshUser, sshKey   string
}

func loadLabE2E(t *testing.T) labE2EEnv {
	t.Helper()
	e := labE2EEnv{
		base:    envOr("SHOAL_BMC_URL", "http://192.168.122.100:8001"),
		user:    os.Getenv("SHOAL_BMC_USERNAME"),
		pass:    os.Getenv("SHOAL_BMC_PASSWORD"),
		iso:     envOr("SHOAL_ISO_URL", "http://192.168.124.1:8080/shoal-marker.iso"),
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

func waitJobState(t *testing.T, store jobstore.Store, id string, want models.LifecycleState, d time.Duration) models.ProvisioningJob {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(d)
	var final models.ProvisioningJob
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
