package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mattcburns/shoal/internal/common/config"
	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/common/secrets"
	"github.com/mattcburns/shoal/internal/common/telemetry"
	"github.com/mattcburns/shoal/internal/deploy/job"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
	"github.com/mattcburns/shoal/internal/observe/sol"
)

func cmdDeploy(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: shoal deploy <run|status|cancel> [flags]")
		return 2
	}
	switch args[0] {
	case "run":
		return cmdDeployRun(args[1:])
	case "status":
		return cmdDeployStatus(args[1:])
	case "cancel":
		return cmdDeployCancel(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown deploy subcommand %q\n", args[0])
		return 2
	}
}

func cmdDeployRun(args []string) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}
	log := newLogger(cfg.LogLevel)

	fs := flag.NewFlagSet("deploy run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	device := fs.String("device", "", "device id (alias for -device-id)")
	deviceID := fs.String("device-id", "", "device id for correlation")
	bmcURL := fs.String("bmc-url", "", "Redfish base URL")
	bmcUser := fs.String("bmc-user", cfg.BMCUsername, "BMC username")
	bmcPass := fs.String("bmc-pass", cfg.BMCPassword, "BMC password (never logged)")
	serial := fs.String("serial-target", "", "libvirt domain or console path")
	isoURL := fs.String("iso-url", "", "BMC-reachable ISO URL on lab :8080")
	systemID := fs.String("system-id", "", "optional Redfish system id or name")
	profile := fs.String("profile-ref", "spike", "profile ref")
	sshHost := fs.String("serial-ssh-host", cfg.SerialSSHHost, "SSH host for nested libvirt serial (VM mode)")
	sshUser := fs.String("serial-ssh-user", cfg.SerialSSHUser, "SSH user for serial delegate")
	sshKey := fs.String("serial-ssh-key", cfg.SerialSSHKey, "SSH private key for serial delegate")
	wait := fs.Bool("wait", true, "wait for terminal job state")
	waitTimeout := fs.Duration("wait-timeout", 30*time.Minute, "max wait when -wait")
	stallTimeout := fs.Duration("stall-timeout", 3*time.Minute, "SOL silence before stall failure")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg.SerialSSHHost = *sshHost
	cfg.SerialSSHUser = *sshUser
	cfg.SerialSSHKey = *sshKey

	dev := *deviceID
	if dev == "" {
		dev = *device
	}
	req := models.StartJobRequest{
		DeviceID:     dev,
		ProfileRef:   *profile,
		ISOURL:       *isoURL,
		BMCEndpoint:  *bmcURL,
		BMCUsername:  *bmcUser,
		BMCPassword:  *bmcPass,
		SerialTarget: *serial,
		SystemID:     *systemID,
		StallTimeout: *stallTimeout,
	}

	store, dbCloser, err := openJobStore(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "jobstore: %v\n", err)
		return 1
	}
	if dbCloser != nil {
		defer dbCloser()
	}

	var secretBackend secrets.Backend = secrets.NewMemory()
	if cfg.SecretsDir != "" {
		fb, err := secrets.NewFile(cfg.SecretsDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "secrets: %v\n", err)
			return 1
		}
		secretBackend = fb
	}

	// Wire Observe watch service with progress from orchestrator (two-step inject).
	watchSvc := sol.NewWatchService(log, nil)
	watchSvc.NewTransport = sol.NewTransportFactory(sol.SSHSerialConfig{
		Host:    cfg.SerialSSHHost,
		User:    cfg.SerialSSHUser,
		KeyPath: cfg.SerialSSHKey,
		UseSudo: cfg.SerialSSHSudo,
	})
	orch := job.NewOrchestrator(job.Options{
		Log:                 log,
		Store:               store,
		Secrets:             secretBackend,
		NewBMC:              redfish.NewBMC,
		Watches:             watchSvc,
		AuthMode:            cfg.RedfishAuthMode,
		TLSMode:             cfg.RedfishTLSMode,
		CAFile:              cfg.RedfishCAFile,
		ReconcileFailOrphan: cfg.ReconcileFailOrphans,
	})
	defer orch.Stop()
	watchSvc.SetProgress(orch.ProgressPort())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := orch.ReconcileOrphans(ctx); err != nil {
		log.Warn("orphan reconcile", "err", err.Error())
	}

	j, err := orch.Start(ctx, req)
	if err != nil {
		// may still have a job row
		fmt.Fprintf(os.Stderr, "deploy run failed: %v\n", err)
		if j.ID != "" {
			_ = json.NewEncoder(os.Stdout).Encode(j)
		}
		return 1
	}
	fmt.Fprintf(os.Stderr, "job %s started (state=%s)\n", j.ID, j.State)
	_ = json.NewEncoder(os.Stdout).Encode(j)

	if !*wait {
		return 0
	}

	deadline := time.Now().Add(*waitTimeout)
	for {
		if ctx.Err() != nil {
			fmt.Fprintln(os.Stderr, "interrupted; job continues until process exit cleanup")
			_ = orch.Cancel(context.Background(), j.ID)
			// give HandleTerminal a moment
			time.Sleep(500 * time.Millisecond)
			return 130
		}
		cur, err := orch.Get(ctx, j.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "status: %v\n", err)
			return 1
		}
		if cur.State == models.StateProvisioned {
			_ = json.NewEncoder(os.Stdout).Encode(cur)
			fmt.Fprintln(os.Stderr, "provisioned OK")
			return 0
		}
		if cur.State == models.StateFailed {
			_ = json.NewEncoder(os.Stdout).Encode(cur)
			fmt.Fprintf(os.Stderr, "failed: %s\n", cur.Error)
			return 1
		}
		if time.Now().After(deadline) {
			fmt.Fprintln(os.Stderr, "wait timeout")
			return 1
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func cmdDeployStatus(args []string) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}
	fs := flag.NewFlagSet("deploy status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jobID := fs.String("job", "", "job id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *jobID == "" {
		fmt.Fprintln(os.Stderr, "-job is required")
		return 2
	}
	store, closer, err := openJobStore(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "jobstore: %v\n", err)
		return 1
	}
	if closer != nil {
		defer closer()
	}
	j, err := store.Get(context.Background(), *jobID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "get job: %v\n", err)
		return 1
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(j)
	return 0
}

func cmdDeployCancel(args []string) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}
	log := newLogger(cfg.LogLevel)
	fs := flag.NewFlagSet("deploy cancel", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jobID := fs.String("job", "", "job id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *jobID == "" {
		fmt.Fprintln(os.Stderr, "-job is required")
		return 2
	}
	store, closer, err := openJobStore(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "jobstore: %v\n", err)
		return 1
	}
	if closer != nil {
		defer closer()
	}
	// Cancel needs a live orchestrator with BMC factory for cleanup.
	var secretBackend secrets.Backend = secrets.NewMemory()
	if cfg.SecretsDir != "" {
		if fb, err := secrets.NewFile(cfg.SecretsDir); err == nil {
			secretBackend = fb
		}
	}
	watchSvc := sol.NewWatchService(log, nil)
	watchSvc.NewTransport = sol.NewTransportFactory(sol.SSHSerialConfig{
		Host:    cfg.SerialSSHHost,
		User:    cfg.SerialSSHUser,
		KeyPath: cfg.SerialSSHKey,
		UseSudo: cfg.SerialSSHSudo,
	})
	orch := job.NewOrchestrator(job.Options{
		Log:                 log,
		Store:               store,
		Secrets:             secretBackend,
		NewBMC:              redfish.NewBMC,
		Watches:             watchSvc,
		AuthMode:            cfg.RedfishAuthMode,
		TLSMode:             cfg.RedfishTLSMode,
		CAFile:              cfg.RedfishCAFile,
		ReconcileFailOrphan: cfg.ReconcileFailOrphans,
	})
	defer orch.Stop()
	watchSvc.SetProgress(orch.ProgressPort())

	if err := orch.Cancel(context.Background(), *jobID); err != nil {
		fmt.Fprintf(os.Stderr, "cancel: %v\n", err)
		return 1
	}
	// Wait briefly for async terminal
	time.Sleep(300 * time.Millisecond)
	j, err := store.Get(context.Background(), *jobID)
	if err == nil {
		_ = json.NewEncoder(os.Stdout).Encode(j)
	}
	return 0
}

func openJobStore(cfg config.Config) (jobstore.Store, func(), error) {
	if cfg.TelemetryDatabaseURL == "" {
		// Process-local store; durable orphans require SHOAL_TELEMETRY_DATABASE_URL.
		return jobstore.NewMemory(), nil, nil
	}
	db, err := telemetry.OpenAndMigrate(context.Background(), cfg.TelemetryDatabaseURL)
	if err != nil {
		return nil, nil, err
	}
	return jobstore.NewPostgres(db), func() { _ = db.Close() }, nil
}
