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
