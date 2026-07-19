package job_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/netbox"
	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/common/secrets"
	"github.com/mattcburns/shoal/internal/deploy/job"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
)

// recordingWatch is a watchport.WatchRegistrar that records the last
// WatchSession passed to Register instead of opening a real transport — it
// isolates "did the orchestrator build the right session fields" from SOL
// transport/marker plumbing, which is covered separately in internal/observe/sol.
type recordingWatch struct {
	mu      sync.Mutex
	last    models.WatchSession
	regErr  error
	regHits int
}

func (r *recordingWatch) Register(_ context.Context, session models.WatchSession) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.last = session
	r.regHits++
	return r.regErr
}

func (r *recordingWatch) Unregister(_ context.Context, _ string) error { return nil }

func (r *recordingWatch) lastSession() models.WatchSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

func newRedfishSOLTestFixtures(t *testing.T) (*jobstore.Memory, *secrets.Memory, *redfish.Fake, *netbox.Memory, *slog.Logger) {
	t.Helper()
	store := jobstore.NewMemory()
	sec := secrets.NewMemory()
	fakeBMC := redfish.NewFake()
	nb := netbox.NewMemory()
	ctx := context.Background()
	_, _ = nb.UpsertDevice(ctx, models.DeviceIdentity{
		Serial: "lab-node-1", LifecycleState: models.StateDiscovered,
	})
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return store, sec, fakeBMC, nb, log
}

// TestOrchestratorRedfishSOLTransportSelectedPerJob proves a per-job
// StartJobRequest.SerialTransport="redfish_sol" override produces a
// WatchSession with Transport/Target/RedfishSystemID/CredentialRef wired from
// req.BMCEndpoint + the resolved system id + the job's credential ref —
// never "libvirt", never a raw password.
func TestOrchestratorRedfishSOLTransportSelectedPerJob(t *testing.T) {
	ctx := context.Background()
	store, sec, fakeBMC, nb, log := newRedfishSOLTestFixtures(t)
	watch := &recordingWatch{}

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

	_, err := orch.Start(ctx, models.StartJobRequest{
		DeviceID:        "lab-node-1",
		BMCEndpoint:     "http://bmc.test",
		BMCUsername:     "admin",
		BMCPassword:     "secret",
		SerialTransport: "redfish_sol",
		ISOURL:          "http://iso/shoal-marker.iso",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	session := watch.lastSession()
	if session.Transport != "redfish_sol" {
		t.Fatalf("transport = %q, want redfish_sol", session.Transport)
	}
	if session.Target != "http://bmc.test" {
		t.Fatalf("target = %q, want bmc_endpoint", session.Target)
	}
	if session.RedfishSystemID == "" {
		t.Fatal("expected RedfishSystemID to be set from the resolved system")
	}
	if session.CredentialRef == "" {
		t.Fatal("expected CredentialRef to be set (never a raw password)")
	}
}

// TestOrchestratorRedfishSOLTransportSelectedByDefault proves the
// orchestrator-wide Options.DefaultSerialTransport applies when a job doesn't
// override it, and that "libvirt" remains the fallback when neither is set.
func TestOrchestratorRedfishSOLTransportSelectedByDefault(t *testing.T) {
	ctx := context.Background()
	store, sec, fakeBMC, nb, log := newRedfishSOLTestFixtures(t)
	watch := &recordingWatch{}

	orch := job.NewOrchestrator(job.Options{
		Log:     log,
		Store:   store,
		Secrets: sec,
		NewBMC: func(cfg redfish.Config) (redfish.BMC, error) {
			return fakeBMC, nil
		},
		Watches:                watch,
		NetBox:                 nb,
		AuthMode:               "basic",
		TLSMode:                "off",
		ReconcileFailOrphan:    true,
		DefaultSerialTransport: "redfish_sol",
	})
	defer orch.Stop()

	_, err := orch.Start(ctx, models.StartJobRequest{
		DeviceID:    "lab-node-1",
		BMCEndpoint: "http://bmc.test",
		BMCUsername: "admin",
		BMCPassword: "secret",
		// validate.StartJobRequest cannot see Options.DefaultSerialTransport (it
		// only validates the request itself), so serial_target is still required
		// here even though the orchestrator will end up ignoring it in favor of
		// bmc_endpoint — this is the documented v1 tradeoff (safe/explicit over
		// clever; see docs/real-hardware-sol-runbook.md). No per-job
		// SerialTransport override is set; the config default should still win.
		SerialTarget: "lab-node-1",
		ISOURL:       "http://iso/shoal-marker.iso",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	session := watch.lastSession()
	if session.Transport != "redfish_sol" {
		t.Fatalf("transport = %q, want redfish_sol (from Options.DefaultSerialTransport)", session.Transport)
	}
	if session.Target != "http://bmc.test" {
		t.Fatalf("target = %q, want bmc_endpoint (serial_target must be ignored for redfish_sol)", session.Target)
	}
}

// TestOrchestratorSerialTransportDefaultsToLibvirt proves that with neither a
// per-job override nor a config default, behavior is unchanged from before
// this feature existed.
func TestOrchestratorSerialTransportDefaultsToLibvirt(t *testing.T) {
	ctx := context.Background()
	store, sec, fakeBMC, nb, log := newRedfishSOLTestFixtures(t)
	watch := &recordingWatch{}

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

	_, err := orch.Start(ctx, models.StartJobRequest{
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

	session := watch.lastSession()
	if session.Transport != "libvirt" {
		t.Fatalf("transport = %q, want libvirt (default unchanged)", session.Transport)
	}
	if session.Target != "lab-node-1" {
		t.Fatalf("target = %q, want serial_target", session.Target)
	}
}
