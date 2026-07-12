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

// TestLabDeployRealBMCInjectedSOL runs Orchestrator against live sushy-tools
// Virtual Media + boot + power, while SOL markers are injected via a pipe
// (nested serial PTY is only on L1; full libvirt SOL is a separate path).
//
// ISO must be BMC-reachable (from nested nodes: http://192.168.124.1:8080/...).
func TestLabDeployRealBMCInjectedSOL(t *testing.T) {
	base := envOr("SHOAL_BMC_URL", "http://192.168.122.100:8001")
	user := os.Getenv("SHOAL_BMC_USERNAME")
	pass := os.Getenv("SHOAL_BMC_PASSWORD")
	if user == "" || pass == "" {
		t.Skip("SHOAL_BMC_USERNAME/PASSWORD required")
	}
	iso := envOr("SHOAL_ISO_URL", "http://192.168.124.1:8080/shoal-marker.iso")
	dsn := os.Getenv("SHOAL_TELEMETRY_DATABASE_URL")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	var store jobstore.Store
	if dsn != "" {
		db, err := telemetry.OpenAndMigrate(ctx, dsn)
		if err != nil {
			t.Fatalf("db: %v", err)
		}
		defer db.Close()
		store = jobstore.NewPostgres(db)
	} else {
		store = jobstore.NewMemory()
	}

	// Resolve shoal-node-1 system id via temporary BMC.
	sysID := os.Getenv("SHOAL_SYSTEM_ID")
	if sysID == "" {
		tmp, err := redfish.NewBMC(redfish.Config{BaseURL: base, Username: user, Password: pass, AuthMode: "basic", TLSMode: "off"})
		if err != nil {
			t.Fatal(err)
		}
		if err := tmp.Open(ctx); err != nil {
			t.Fatal(err)
		}
		systems, err := tmp.ListSystems(ctx)
		_ = tmp.Close(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range systems {
			if s.Name == "shoal-node-1" {
				sysID = s.ID
				break
			}
		}
		if sysID == "" && len(systems) > 0 {
			sysID = systems[0].ID
		}
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pr, pw := io.Pipe()
	watch := sol.NewWatchService(log, nil)
	watch.NewTransport = func(models.WatchSession) sol.Transport {
		return sol.NewReaderTransport(pr)
	}

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
	defer orch.Stop()
	watch.SetProgress(orch.ProgressPort())

	j, err := orch.Start(ctx, models.StartJobRequest{
		DeviceID:     "shoal-node-1",
		BMCEndpoint:  base,
		BMCUsername:  user,
		BMCPassword:  pass,
		SerialTarget: "injected-pipe",
		ISOURL:       iso,
		SystemID:     sysID,
		ProfileRef:   "lab-spike",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Logf("job %s started", j.ID)

	// Verify media inserted on live BMC
	check, err := redfish.NewBMC(redfish.Config{BaseURL: base, Username: user, Password: pass, AuthMode: "basic", TLSMode: "off"})
	if err != nil {
		t.Fatal(err)
	}
	if err := check.Open(ctx); err != nil {
		t.Fatal(err)
	}
	vms, err := check.ListVirtualMedia(ctx, sysID)
	if err != nil {
		t.Fatal(err)
	}
	inserted := false
	for _, vm := range vms {
		if vm.Inserted {
			inserted = true
			t.Logf("inserted media %s image=%s", vm.URI, vm.Image)
		}
	}
	if !inserted {
		t.Fatal("expected virtual media inserted on lab BMC")
	}

	// Inject SOL markers → DONE
	for _, line := range []string{
		"SHOAL|1|1|2026-07-10T00:00:00Z|BOOT|5|OK|lab boot",
		"SHOAL|1|2|2026-07-10T00:00:01Z|IMAGE_WRITE|50|OK|lab write",
		"SHOAL|1|3|2026-07-10T00:00:02Z|DONE|100|OK|lab done",
	} {
		if _, err := io.WriteString(pw, line+"\n"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = pw.Close()

	deadline := time.Now().Add(30 * time.Second)
	var final models.ProvisioningJob
	for time.Now().Before(deadline) {
		final, err = store.Get(ctx, j.ID)
		if err != nil {
			t.Fatal(err)
		}
		if final.State == models.StateProvisioned || final.State == models.StateFailed {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if final.State != models.StateProvisioned {
		t.Fatalf("want provisioned, got %s err=%q phase=%s", final.State, final.Error, final.Phase)
	}

	// Cleanup on BMC
	vms, _ = check.ListVirtualMedia(ctx, sysID)
	for _, vm := range vms {
		if vm.Inserted {
			t.Fatalf("media still inserted after DONE cleanup: %s", vm.URI)
		}
	}
	boot, _ := check.GetBoot(ctx, sysID)
	t.Logf("final boot state: %+v", boot)
	_ = check.Close(context.Background())
	t.Logf("job %s provisioned OK", final.ID)
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
