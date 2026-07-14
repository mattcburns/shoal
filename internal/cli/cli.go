// Package cli implements flag-based subcommands for the shoal binary.
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mattcburns/shoal/internal/api"
	"github.com/mattcburns/shoal/internal/common/config"
	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/netbox"
	"github.com/mattcburns/shoal/internal/common/redact"
	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/common/secrets"
	"github.com/mattcburns/shoal/internal/common/telemetry"
	"github.com/mattcburns/shoal/internal/core/ai"
	"github.com/mattcburns/shoal/internal/core/fewshot"
	"github.com/mattcburns/shoal/internal/core/reconcile"
	"github.com/mattcburns/shoal/internal/deploy/job"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
	"github.com/mattcburns/shoal/internal/discover"
	"github.com/mattcburns/shoal/internal/observe"
	"github.com/mattcburns/shoal/internal/observe/poll"
	"github.com/mattcburns/shoal/internal/observe/sol"
)

// Version is the application version string (overridable via -ldflags).
var Version = "0.4.0-phase4"

// Run dispatches subcommands. args should be os.Args[1:].
func Run(args []string) int {
	if len(args) < 1 {
		printUsage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "version":
		return cmdVersion(os.Stdout)
	case "serve":
		return cmdServe(args[1:])
	case "deploy":
		return cmdDeploy(args[1:])
	case "discover":
		return cmdDiscover(args[1:])
	case "observe":
		return cmdObserve(args[1:])
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		printUsage(os.Stderr)
		return 2
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `shoal — BMC-centric bare-metal lifecycle

Usage:
  shoal <command> [flags]

Commands:
  version    Print version and exit
  serve      Run the HTTP API server
  deploy     Provisioning: run | status | cancel
  discover   Assets: ingest | confirm
  observe    Status / poll: status | poll

Phase 4 observe example:
  export SHOAL_TELEMETRY_DATABASE_URL=postgres://…@192.168.122.100:5433/shoal_telemetry
  shoal observe status -device-id shoal-node-1
  shoal observe poll -device-id shoal-node-1 -bmc-url http://192.168.122.100:8001

Phase 3 discover example:
  export SHOAL_AI_PROVIDER=ollama
  export SHOAL_AI_MODEL=llama3.2:3b
  export SHOAL_OLLAMA_URL=http://192.168.122.100:11434
  export SHOAL_NETBOX_URL=http://192.168.122.100:8000
  export SHOAL_NETBOX_TOKEN=…
  shoal discover ingest -kind redfish_json -file dump.json -bmc-ip 192.168.122.100

Phase 2 deploy example (VM lab):
  export SHOAL_SERIAL_SSH_HOST=192.168.122.100
  export SHOAL_SERIAL_SSH_KEY=$HOME/.ssh/shoal_lab_vm
  shoal deploy run \
    -device-id shoal-node-1 \
    -bmc-url http://192.168.122.100:8001 \
    -bmc-user "$SHOAL_BMC_USERNAME" \
    -bmc-pass "$SHOAL_BMC_PASSWORD" \
    -serial-target shoal-node-1 \
    -iso-url http://192.168.124.1:8080/shoal-marker.iso

`)
}

func cmdVersion(w io.Writer) int {
	fmt.Fprintln(w, Version)
	return 0
}

func cmdServe(args []string) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}

	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	addr := fs.String("addr", cfg.HTTPAddr, "HTTP listen address")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg.HTTPAddr = *addr

	log := newLogger(cfg.LogLevel)
	slog.SetDefault(log)

	srvAPI := api.New(cfg, log)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Optional Discover / AI wiring (Phase 3 + 3b learning). No ai.Fake for Observe.
	var fsStore fewshot.Store
	var rec reconcile.Reconciler
	if cfg.FewShotDir != "" {
		if st, err := fewshot.NewFileStore(cfg.FewShotDir); err != nil {
			log.Warn("fewshot store unavailable", "err", err.Error())
		} else {
			fsStore = st
			log.Info("fewshot learning enabled", "dir", cfg.FewShotDir)
		}
	}
	if llm, err := ai.NewFromConfig(cfg); err != nil {
		log.Warn("ai client not configured", "err", err.Error())
	} else if llm != nil {
		r, err := reconcile.NewWithFewShot(llm, log, fsStore)
		if err != nil {
			log.Warn("reconciler init failed", "err", err.Error())
		} else {
			rec = r
			var nb netbox.API
			if cfg.NetBoxURL != "" && cfg.NetBoxToken != "" {
				nb = netbox.New(cfg.NetBoxURL, cfg.NetBoxToken)
			}
			disc := discover.NewWithFewShot(log, rec, openSecrets(cfg), nb, fsStore)
			srvAPI.WithDiscover(disc)
			log.Info("discover ingest/confirm API enabled")
		}
	}

	store, closer, err := openJobStore(cfg)
	if err != nil {
		log.Warn("job store unavailable for API", "err", err.Error())
	} else {
		if closer != nil {
			defer closer()
		}
		srvAPI.WithJobStore(store)
		// Wire Orchestrator so cancel works and orphans are reconciled on boot.
		secretBackend := openSecrets(cfg)
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
		srvAPI.WithJobCanceler(orch)
		if err := orch.ReconcileOrphans(ctx); err != nil {
			log.Warn("orphan reconcile", "err", err.Error())
		}

		// Phase 4: Observe status. Telemetry is Postgres-only — no silent memory fallback.
		var telemStore telemetry.Store
		if cfg.TelemetryDatabaseURL != "" {
			db, err := telemetry.OpenAndMigrate(ctx, cfg.TelemetryDatabaseURL)
			if err != nil {
				log.Error("telemetry store open failed; observe events/poll disabled", "err", err.Error())
			} else {
				defer db.Close()
				telemStore = telemetry.NewPostgres(db)
			}
		} else {
			log.Warn("SHOAL_TELEMETRY_DATABASE_URL unset; observe events/poll disabled")
		}
		obsSvc := observe.New(log, store, telemStore, watchSvc)
		srvAPI.WithObserve(obsSvc)
		log.Info("observe device status API enabled", "telemetry", telemStore != nil)

		// Background SEL/sensor poll only with durable telemetry.
		if telemStore != nil {
			poller := poll.New(log, telemStore, redfish.NewBMC)
			poller.Watching = watchSvc
			// Use real Core reconciler when AI is configured; else deterministic poll path.
			if rec != nil {
				poller.Events = rec
			}
			seedPollTargets(ctx, poller, store, cfg)
			go func() {
				ticker := time.NewTicker(30 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						seedPollTargets(ctx, poller, store, cfg)
					}
				}
			}()
			go poller.Run(ctx)
			log.Info("observe SEL/sensor poller started",
				"idle", poll.DefaultIdleInterval.String(),
				"watch", poll.DefaultWatchInterval.String(),
			)
		}
	}

	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srvAPI.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("shoal serve listening", "addr", cfg.HTTPAddr, "version", Version)
		errCh <- httpSrv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			log.Error("shutdown error", "err", err.Error())
			return 1
		}
		return 0
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			log.Error("server error", "err", err.Error())
			return 1
		}
		return 0
	}
}

func openSecrets(cfg config.Config) secrets.Backend {
	if cfg.SecretsDir != "" {
		if fb, err := secrets.NewFile(cfg.SecretsDir); err == nil {
			return fb
		}
	}
	return secrets.NewMemory()
}

// seedPollTargets registers BMC endpoints from durable jobs for SEL/sensor poll.
func seedPollTargets(ctx context.Context, p *poll.Poller, store jobstore.Store, cfg config.Config) {
	if p == nil || store == nil {
		return
	}
	states := []models.LifecycleState{
		models.StateProvisioning,
		models.StateProvisioned,
		models.StateFailed,
	}
	for _, st := range states {
		list, err := store.ListByState(ctx, st)
		if err != nil {
			continue
		}
		for _, j := range list {
			if j.BMCEndpoint == "" || j.DeviceID == "" {
				continue
			}
			_ = p.SetTarget(poll.Target{
				DeviceID: j.DeviceID,
				BMC: redfish.Config{
					BaseURL:  j.BMCEndpoint,
					Username: cfg.BMCUsername,
					Password: cfg.BMCPassword,
					AuthMode: cfg.RedfishAuthMode,
					TLSMode:  cfg.RedfishTLSMode,
					CAFile:   cfg.RedfishCAFile,
				},
			})
		}
	}
}

func newLogger(level string) *slog.Logger {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level:       lv,
		ReplaceAttr: redact.ReplaceAttr,
	})
	return slog.New(h)
}
