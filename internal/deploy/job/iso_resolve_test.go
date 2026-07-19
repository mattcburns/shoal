package job_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/common/secrets"
	"github.com/mattcburns/shoal/internal/core/profile"
	"github.com/mattcburns/shoal/internal/deploy/job"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
	"github.com/mattcburns/shoal/internal/observe/sol"
)

func TestStartResolvesISOURLFromProfile(t *testing.T) {
	ctx := context.Background()
	ps := profile.NewMemory()
	_, err := ps.Save(ctx, models.ProvisioningProfile{
		Ref: "lab-ubuntu", ISOBase: "shoal-marker", NeedsApproval: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pr, _ := io.Pipe()
	watch := sol.NewWatchService(log, nil)
	watch.NewTransport = func(session models.WatchSession) sol.Transport {
		return sol.NewReaderTransport(pr)
	}
	orch := job.NewOrchestrator(job.Options{
		Log: log, Store: jobstore.NewMemory(), Secrets: secrets.NewMemory(),
		Profiles: ps, ISOBaseURL: "http://192.168.124.1:8080",
		NewBMC:  func(cfg redfish.Config) (redfish.BMC, error) { return redfish.NewFake(), nil },
		Watches: watch, AuthMode: "basic", TLSMode: "off",
	})
	defer orch.Stop()
	watch.SetProgress(orch.ProgressPort())

	j, err := orch.Start(ctx, models.StartJobRequest{
		DeviceID: "d1", ProfileRef: "lab-ubuntu",
		// ISOURL intentionally empty
		BMCEndpoint: "http://bmc", BMCUsername: "a", BMCPassword: "b",
		SerialTarget: "d1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if j.ISOURL != "http://192.168.124.1:8080/shoal-marker.iso" {
		t.Fatalf("iso_url %q", j.ISOURL)
	}
}

func TestStartProfileOnlyImageWrite(t *testing.T) {
	// M6: strategy/family from profile; no iso_url or install_strategy on request.
	ctx := context.Background()
	ps := profile.NewMemory()
	_, err := ps.Save(ctx, models.ProvisioningProfile{
		Ref:             "lab-ubuntu-image-write",
		ISOBase:         "shoal-ubuntu-m1-test",
		InstallStrategy: models.InstallStrategyImageWrite,
		OSFamily:        models.OSFamilyUbuntu,
		SeedDelivery:    models.SeedDeliveryNone,
		Prep:            "skip",
		NeedsApproval:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pr, pw := io.Pipe()
	watch := sol.NewWatchService(log, nil)
	watch.NewTransport = func(session models.WatchSession) sol.Transport {
		return sol.NewReaderTransport(pr)
	}
	store := jobstore.NewMemory()
	orch := job.NewOrchestrator(job.Options{
		Log: log, Store: store, Secrets: secrets.NewMemory(),
		Profiles: ps, ISOBaseURL: "http://192.168.124.1:8080",
		NewBMC:  func(cfg redfish.Config) (redfish.BMC, error) { return redfish.NewFake(), nil },
		Watches: watch, AuthMode: "basic", TLSMode: "off",
	})
	defer orch.Stop()
	watch.SetProgress(orch.ProgressPort())

	j, err := orch.Start(ctx, models.StartJobRequest{
		DeviceID:     "lab-node-1",
		ProfileRef:   "lab-ubuntu-image-write",
		BMCEndpoint:  "http://bmc",
		BMCUsername:  "a",
		BMCPassword:  "b",
		SerialTarget: "lab-node-1",
		// no ISOURL, InstallStrategy, OsFamily, Prep
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if j.InstallStrategy != models.InstallStrategyImageWrite {
		t.Fatalf("strategy=%s", j.InstallStrategy)
	}
	if j.ISOURL != "http://192.168.124.1:8080/shoal-ubuntu-m1-test.iso" {
		t.Fatalf("iso=%s", j.ISOURL)
	}
	if len(j.Stages) != 1 || j.Stages[0].Kind != models.JobStageKindOSInstall {
		t.Fatalf("stages=%+v", j.Stages)
	}
	// finish so test does not leak
	_, _ = io.WriteString(pw, "SHOAL|1|1|2026-06-19T04:10:00Z|DONE|100|OK|done\n")
	_ = pw.Close()
}

func TestStartProfileOperatorISO(t *testing.T) {
	ctx := context.Background()
	ps := profile.NewMemory()
	_, err := ps.Save(ctx, models.ProvisioningProfile{
		Ref:             "esxi-fleet",
		MediaURL:        "http://lab:8080/esxi-custom.iso",
		InstallStrategy: models.InstallStrategyOperatorISO,
		OSFamily:        models.OSFamilyESXi,
		SeedDelivery:    models.SeedDeliveryNone,
		StageTimeout:    "100ms",
		NeedsApproval:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	watch := sol.NewWatchService(log, nil)
	watch.NewTransport = func(session models.WatchSession) sol.Transport {
		return sol.NewReaderTransport(io.NopCloser(strings.NewReader("")))
	}
	store := jobstore.NewMemory()
	orch := job.NewOrchestrator(job.Options{
		Log: log, Store: store, Secrets: secrets.NewMemory(),
		Profiles: ps, ISOBaseURL: "http://lab:8080",
		NewBMC:  func(cfg redfish.Config) (redfish.BMC, error) { return redfish.NewFake(), nil },
		Watches: watch, AuthMode: "basic", TLSMode: "off",
	})
	defer orch.Stop()
	watch.SetProgress(orch.ProgressPort())

	j, err := orch.Start(ctx, models.StartJobRequest{
		DeviceID:    "esxi-1",
		ProfileRef:  "esxi-fleet",
		BMCEndpoint: "http://bmc",
		BMCUsername: "a",
		BMCPassword: "b",
		// no serial, no iso_url — coarse operator_iso from profile
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if j.InstallStrategy != models.InstallStrategyOperatorISO {
		t.Fatalf("strategy=%s", j.InstallStrategy)
	}
	if j.ISOURL != "http://lab:8080/esxi-custom.iso" {
		t.Fatalf("iso=%s", j.ISOURL)
	}
}

func TestStartRejectsMissingISOWithoutResolvableProfile(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pr, _ := io.Pipe()
	watch := sol.NewWatchService(log, nil)
	watch.NewTransport = func(session models.WatchSession) sol.Transport {
		return sol.NewReaderTransport(pr)
	}
	orch := job.NewOrchestrator(job.Options{
		Log: log, Store: jobstore.NewMemory(), Secrets: secrets.NewMemory(),
		NewBMC:  func(cfg redfish.Config) (redfish.BMC, error) { return redfish.NewFake(), nil },
		Watches: watch, AuthMode: "basic", TLSMode: "off",
	})
	defer orch.Stop()
	watch.SetProgress(orch.ProgressPort())
	_, err := orch.Start(ctx, models.StartJobRequest{
		DeviceID: "d1", ProfileRef: "spike",
		BMCEndpoint: "http://bmc", BMCUsername: "a", BMCPassword: "b",
		SerialTarget: "d1",
	})
	if err == nil {
		t.Fatal("expected iso_url required")
	}
}
