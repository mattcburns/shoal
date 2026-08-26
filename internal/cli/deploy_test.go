package cli

import (
	"log/slog"
	"testing"

	"github.com/mattcburns/shoal/internal/common/config"
	"github.com/mattcburns/shoal/internal/common/secrets"
	"github.com/mattcburns/shoal/internal/common/telemetry"
	"github.com/mattcburns/shoal/internal/core/profile"
	"github.com/mattcburns/shoal/internal/deploy/iso"
	"github.com/mattcburns/shoal/internal/deploy/job"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
	"github.com/mattcburns/shoal/internal/observe/sol"
)

// TestBuildOrchestratorOptionsSharesCommonFields pins the fix for the
// job.Options{} drift across cmdDeployRun/cmdDeployCancel/cmdDeployDeprovision:
// each used to hand-build its own literal and picked up common fields
// (AuthMode/TLSMode/CAFile/ISOBaseURL/ISOPublishDir/ISODynamic/
// ReconcileFailOrphan/DefaultSerialTransport) inconsistently as new Options
// fields were added over time. All three commands now call
// buildOrchestratorOptions, so this test drives it exactly as each command
// does and checks the common fields always come through, while the
// deliberately per-command deps (Telemetry/Profiles/ISOBuilder) only appear
// when the caller actually supplies them.
func TestBuildOrchestratorOptionsSharesCommonFields(t *testing.T) {
	cfg := config.Config{
		ISOBaseURL:           "http://iso.example",
		ISOPublishDir:        "/tmp/shoal-iso",
		ISODynamic:           true,
		RedfishAuthMode:      "session",
		RedfishTLSMode:       "verify",
		RedfishCAFile:        "/tmp/ca.pem",
		ReconcileFailOrphans: true,
		SerialTransport:      "redfish_sol",
	}
	log := slog.Default()
	store := jobstore.NewMemory()
	sec := secrets.NewMemory()
	watchSvc := sol.NewWatchService(log, nil)

	checkCommon := func(t *testing.T, opts job.Options) {
		t.Helper()
		if opts.ISOBaseURL != cfg.ISOBaseURL {
			t.Errorf("ISOBaseURL = %q, want %q", opts.ISOBaseURL, cfg.ISOBaseURL)
		}
		if opts.ISOPublishDir != cfg.ISOPublishDir {
			t.Errorf("ISOPublishDir = %q, want %q", opts.ISOPublishDir, cfg.ISOPublishDir)
		}
		if opts.ISODynamic != cfg.ISODynamic {
			t.Errorf("ISODynamic = %v, want %v", opts.ISODynamic, cfg.ISODynamic)
		}
		if opts.AuthMode != cfg.RedfishAuthMode {
			t.Errorf("AuthMode = %q, want %q", opts.AuthMode, cfg.RedfishAuthMode)
		}
		if opts.TLSMode != cfg.RedfishTLSMode {
			t.Errorf("TLSMode = %q, want %q", opts.TLSMode, cfg.RedfishTLSMode)
		}
		if opts.CAFile != cfg.RedfishCAFile {
			t.Errorf("CAFile = %q, want %q", opts.CAFile, cfg.RedfishCAFile)
		}
		if opts.ReconcileFailOrphan != cfg.ReconcileFailOrphans {
			t.Errorf("ReconcileFailOrphan = %v, want %v", opts.ReconcileFailOrphan, cfg.ReconcileFailOrphans)
		}
		if opts.DefaultSerialTransport != cfg.SerialTransport {
			t.Errorf("DefaultSerialTransport = %q, want %q", opts.DefaultSerialTransport, cfg.SerialTransport)
		}
		if opts.NewBMC == nil {
			t.Error("NewBMC not set")
		}
		if opts.Store == nil || opts.Secrets == nil || opts.Watches == nil {
			t.Errorf("core deps missing: store=%v secrets=%v watches=%v", opts.Store, opts.Secrets, opts.Watches)
		}
	}

	newWorkingOrchestrator := func(t *testing.T, opts job.Options) {
		t.Helper()
		orch := job.NewOrchestrator(opts)
		if orch == nil {
			t.Fatal("NewOrchestrator returned nil")
		}
		defer orch.Stop()
		if orch.ProgressPort() == nil {
			t.Error("ProgressPort() is nil")
		}
	}

	t.Run("run includes Telemetry/Profiles/ISOBuilder", func(t *testing.T) {
		telem := telemetry.NewMemory()
		prof := profile.NewMemory()
		isoBuilder := iso.NewScriptBuilder("", log)
		opts := buildOrchestratorOptions(cfg, log, deployOrchestratorDeps{
			Store:      store,
			Secrets:    sec,
			Watches:    watchSvc,
			Telemetry:  telem,
			Profiles:   prof,
			ISOBuilder: isoBuilder,
		})
		checkCommon(t, opts)
		if opts.Telemetry != telem {
			t.Error("Telemetry not carried through for cmdDeployRun-shaped deps")
		}
		if opts.Profiles != prof {
			t.Error("Profiles not carried through for cmdDeployRun-shaped deps")
		}
		if opts.ISOBuilder != isoBuilder {
			t.Error("ISOBuilder not carried through for cmdDeployRun-shaped deps")
		}
		newWorkingOrchestrator(t, opts)
	})

	t.Run("cancel omits Telemetry/Profiles/ISOBuilder", func(t *testing.T) {
		opts := buildOrchestratorOptions(cfg, log, deployOrchestratorDeps{
			Store:   store,
			Secrets: sec,
			Watches: watchSvc,
		})
		checkCommon(t, opts)
		if opts.Telemetry != nil || opts.Profiles != nil || opts.ISOBuilder != nil {
			t.Errorf("cmdDeployCancel-shaped deps should leave Telemetry/Profiles/ISOBuilder nil: %+v", opts)
		}
		newWorkingOrchestrator(t, opts)
	})

	t.Run("deprovision includes Telemetry but omits Profiles/ISOBuilder", func(t *testing.T) {
		telem := telemetry.NewMemory()
		opts := buildOrchestratorOptions(cfg, log, deployOrchestratorDeps{
			Store:     store,
			Secrets:   sec,
			Watches:   watchSvc,
			Telemetry: telem,
		})
		checkCommon(t, opts)
		if opts.Telemetry != telem {
			t.Error("Telemetry not carried through for cmdDeployDeprovision-shaped deps")
		}
		if opts.Profiles != nil || opts.ISOBuilder != nil {
			t.Errorf("cmdDeployDeprovision-shaped deps should leave Profiles/ISOBuilder nil: %+v", opts)
		}
		newWorkingOrchestrator(t, opts)
	})
}
