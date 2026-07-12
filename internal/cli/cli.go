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
	"github.com/mattcburns/shoal/internal/common/redact"
	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/common/secrets"
	"github.com/mattcburns/shoal/internal/deploy/job"
	"github.com/mattcburns/shoal/internal/observe/sol"
)

// Version is the application version string (overridable via -ldflags).
var Version = "0.2.0-phase2"

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
  version   Print version and exit
  serve     Run the HTTP API server
  deploy    Provisioning: run | status | cancel

Phase 2 example (VM lab):
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
