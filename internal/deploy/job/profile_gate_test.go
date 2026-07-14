package job_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/common/secrets"
	"github.com/mattcburns/shoal/internal/core/profile"
	"github.com/mattcburns/shoal/internal/deploy/job"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
	"github.com/mattcburns/shoal/internal/observe/sol"
)

func TestStartRejectsUnapprovedDestructProfile(t *testing.T) {
	ctx := context.Background()
	store := jobstore.NewMemory()
	sec := secrets.NewMemory()
	ps := profile.NewMemory()
	_, err := ps.Save(ctx, models.ProvisioningProfile{
		Ref: "wipe-me", ISOBase: "ubuntu-live",
		DestructSteps: []string{"wipe disk"}, NeedsApproval: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pr, pw := io.Pipe()
	_ = pw
	watch := sol.NewWatchService(log, nil)
	watch.NewTransport = func(session models.WatchSession) sol.Transport {
		return sol.NewReaderTransport(pr)
	}
	orch := job.NewOrchestrator(job.Options{
		Log: log, Store: store, Secrets: sec, Profiles: ps,
		NewBMC:  func(cfg redfish.Config) (redfish.BMC, error) { return redfish.NewFake(), nil },
		Watches: watch, AuthMode: "basic", TLSMode: "off",
	})
	defer orch.Stop()
	watch.SetProgress(orch.ProgressPort())

	_, err = orch.Start(ctx, models.StartJobRequest{
		DeviceID: "d1", ProfileRef: "wipe-me",
		BMCEndpoint: "http://bmc", BMCUsername: "a", BMCPassword: "b",
		SerialTarget: "d1", ISOURL: "http://iso/x.iso",
	})
	if err == nil {
		t.Fatal("expected approval error")
	}

	// Consent via flag — need a fresh pipe transport for second start
	pr2, _ := io.Pipe()
	watch.NewTransport = func(session models.WatchSession) sol.Transport {
		return sol.NewReaderTransport(pr2)
	}
	j, err := orch.Start(ctx, models.StartJobRequest{
		DeviceID: "d1", ProfileRef: "wipe-me", ApproveDestruct: true,
		BMCEndpoint: "http://bmc", BMCUsername: "a", BMCPassword: "b",
		SerialTarget: "d1", ISOURL: "http://iso/x.iso",
	})
	if err != nil {
		t.Fatalf("approve-destruct should allow start: %v", err)
	}
	if j.State != models.StateProvisioning {
		t.Fatalf("%s", j.State)
	}
}

func TestStartSpikeSkipsProfileStore(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pr, _ := io.Pipe()
	watch := sol.NewWatchService(log, nil)
	watch.NewTransport = func(session models.WatchSession) sol.Transport {
		return sol.NewReaderTransport(pr)
	}
	orch := job.NewOrchestrator(job.Options{
		Log: log, Store: jobstore.NewMemory(), Secrets: secrets.NewMemory(),
		// no Profiles store
		NewBMC:  func(cfg redfish.Config) (redfish.BMC, error) { return redfish.NewFake(), nil },
		Watches: watch, AuthMode: "basic", TLSMode: "off",
	})
	defer orch.Stop()
	watch.SetProgress(orch.ProgressPort())
	_, err := orch.Start(ctx, models.StartJobRequest{
		DeviceID: "d1", ProfileRef: "spike",
		BMCEndpoint: "http://bmc", BMCUsername: "a", BMCPassword: "b",
		SerialTarget: "d1", ISOURL: "http://iso/x.iso",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStartAllowsStoreApprovedProfile(t *testing.T) {
	ctx := context.Background()
	store := jobstore.NewMemory()
	sec := secrets.NewMemory()
	ps := profile.NewMemory()
	_, err := ps.Save(ctx, models.ProvisioningProfile{
		Ref: "wipe-ok", ISOBase: "ubuntu-live",
		DestructSteps: []string{"wipe disk"}, NeedsApproval: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ps.Approve(ctx, "wipe-ok", "tester"); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pr, _ := io.Pipe()
	watch := sol.NewWatchService(log, nil)
	watch.NewTransport = func(session models.WatchSession) sol.Transport {
		return sol.NewReaderTransport(pr)
	}
	orch := job.NewOrchestrator(job.Options{
		Log: log, Store: store, Secrets: sec, Profiles: ps,
		NewBMC:  func(cfg redfish.Config) (redfish.BMC, error) { return redfish.NewFake(), nil },
		Watches: watch, AuthMode: "basic", TLSMode: "off",
	})
	defer orch.Stop()
	watch.SetProgress(orch.ProgressPort())
	j, err := orch.Start(ctx, models.StartJobRequest{
		DeviceID: "d1", ProfileRef: "wipe-ok",
		BMCEndpoint: "http://bmc", BMCUsername: "a", BMCPassword: "b",
		SerialTarget: "d1", ISOURL: "http://iso/x.iso",
	})
	if err != nil {
		t.Fatalf("store-approved profile should start: %v", err)
	}
	if j.State != models.StateProvisioning {
		t.Fatalf("%s", j.State)
	}
}

func TestStartRejectsMissingNonSpikeProfile(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pr, _ := io.Pipe()
	watch := sol.NewWatchService(log, nil)
	watch.NewTransport = func(session models.WatchSession) sol.Transport {
		return sol.NewReaderTransport(pr)
	}
	orch := job.NewOrchestrator(job.Options{
		Log: log, Store: jobstore.NewMemory(), Secrets: secrets.NewMemory(),
		Profiles: profile.NewMemory(),
		NewBMC:   func(cfg redfish.Config) (redfish.BMC, error) { return redfish.NewFake(), nil },
		Watches:  watch, AuthMode: "basic", TLSMode: "off",
	})
	defer orch.Stop()
	watch.SetProgress(orch.ProgressPort())
	_, err := orch.Start(ctx, models.StartJobRequest{
		DeviceID: "d1", ProfileRef: "does-not-exist",
		BMCEndpoint: "http://bmc", BMCUsername: "a", BMCPassword: "b",
		SerialTarget: "d1", ISOURL: "http://iso/x.iso",
	})
	if err == nil {
		t.Fatal("expected missing profile error")
	}
}
