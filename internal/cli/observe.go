package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/mattcburns/shoal/internal/common/config"
	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/common/telemetry"
	"github.com/mattcburns/shoal/internal/observe"
	"github.com/mattcburns/shoal/internal/observe/poll"
)

func cmdObserve(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: shoal observe <status|poll> [flags]")
		return 2
	}
	switch args[0] {
	case "status":
		return cmdObserveStatus(args[1:])
	case "poll":
		return cmdObservePoll(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown observe subcommand %q\n", args[0])
		return 2
	}
}

func cmdObserveStatus(args []string) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}
	fs := flag.NewFlagSet("observe status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	deviceID := fs.String("device-id", "", "device id (required)")
	events := fs.Int("events", 0, "include last N telemetry events (requires SHOAL_TELEMETRY_DATABASE_URL)")
	bmcURL := fs.String("bmc-url", "", "optional Redfish base URL for power state")
	bmcUser := fs.String("bmc-user", cfg.BMCUsername, "BMC username")
	bmcPass := fs.String("bmc-pass", cfg.BMCPassword, "BMC password")
	systemID := fs.String("system-id", "", "Redfish system id (required with multi-system BMC + -bmc-url)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *deviceID == "" {
		fmt.Fprintln(os.Stderr, "observe status: -device-id required")
		return 2
	}

	log := newLogger(cfg.LogLevel)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	store, closer, err := openJobStore(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "job store: %v\n", err)
		return 1
	}
	if closer != nil {
		defer closer()
	}

	telem, telemCloser, err := openTelemetryStore(ctx, cfg, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "telemetry: %v\n", err)
		return 1
	}
	if telemCloser != nil {
		defer telemCloser()
	}
	if *events > 0 && telem == nil {
		fmt.Fprintln(os.Stderr, "observe status: -events requires SHOAL_TELEMETRY_DATABASE_URL")
		return 1
	}

	svc := observe.New(log, store, telem, nil)
	st, err := svc.Status(ctx, *deviceID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "status: %v\n", err)
		return 1
	}

	if *bmcURL != "" {
		bmc, err := redfish.NewBMC(redfish.Config{
			BaseURL:  *bmcURL,
			Username: *bmcUser,
			Password: *bmcPass,
			AuthMode: cfg.RedfishAuthMode,
			TLSMode:  cfg.RedfishTLSMode,
			CAFile:   cfg.RedfishCAFile,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "bmc: %v\n", err)
			return 1
		}
		if err := bmc.Open(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "bmc open: %v\n", err)
			return 1
		}
		defer func() { _ = bmc.Close(context.Background()) }()
		st, err = svc.StatusWithPower(ctx, *deviceID, bmc, *systemID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "power state: %v\n", err)
			return 1
		}
	}

	out := map[string]any{
		"status":   st,
		"watching": svc.Watching(*deviceID),
	}
	if *events > 0 {
		evs, err := svc.ListEvents(ctx, *deviceID, time.Time{}, *events)
		if err != nil {
			fmt.Fprintf(os.Stderr, "events: %v\n", err)
			return 1
		}
		if evs == nil {
			evs = []models.NormalizedEvent{}
		}
		out["events"] = evs
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
	return 0
}

func cmdObservePoll(args []string) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}
	fs := flag.NewFlagSet("observe poll", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	deviceID := fs.String("device-id", "", "device id (required)")
	bmcURL := fs.String("bmc-url", "", "Redfish base URL (required)")
	bmcUser := fs.String("bmc-user", cfg.BMCUsername, "BMC username")
	bmcPass := fs.String("bmc-pass", cfg.BMCPassword, "BMC password")
	systemID := fs.String("system-id", "", "optional Redfish system id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *deviceID == "" || *bmcURL == "" {
		fmt.Fprintln(os.Stderr, "observe poll: -device-id and -bmc-url required")
		return 2
	}

	log := newLogger(cfg.LogLevel)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Durable store required — no silent memory fallback.
	telem, closer, err := openTelemetryStore(ctx, cfg, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "telemetry: %v\n", err)
		return 1
	}
	if closer != nil {
		defer closer()
	}

	// Deterministic normalize only (no ai.Fake). Core reconciler optional later.
	p := poll.New(log, telem, redfish.NewBMC)
	selN, sensN, err := p.PollOnce(ctx, poll.Target{
		DeviceID: *deviceID,
		SystemID: *systemID,
		BMC: redfish.Config{
			BaseURL:  *bmcURL,
			Username: *bmcUser,
			Password: *bmcPass,
			AuthMode: cfg.RedfishAuthMode,
			TLSMode:  cfg.RedfishTLSMode,
			CAFile:   cfg.RedfishCAFile,
		},
	})
	out := map[string]any{
		"device_id":       *deviceID,
		"sel_new":         selN,
		"sensors_written": sensN,
	}
	if err != nil {
		out["error"] = err.Error()
		_ = json.NewEncoder(os.Stdout).Encode(out)
		fmt.Fprintf(os.Stderr, "poll: %v\n", err)
		return 1
	}
	_ = json.NewEncoder(os.Stdout).Encode(out)
	return 0
}

// openTelemetryStore opens Postgres when DSN is set.
// requireDSN=true → error if unset or open fails.
// requireDSN=false → nil store if unset; error if set but open fails (no memory fallback).
func openTelemetryStore(ctx context.Context, cfg config.Config, requireDSN bool) (telemetry.Store, func(), error) {
	if cfg.TelemetryDatabaseURL == "" {
		if requireDSN {
			return nil, nil, fmt.Errorf("SHOAL_TELEMETRY_DATABASE_URL is required")
		}
		return nil, nil, nil
	}
	db, err := telemetry.OpenAndMigrate(ctx, cfg.TelemetryDatabaseURL)
	if err != nil {
		return nil, nil, err
	}
	return telemetry.NewPostgres(db), func() { _ = db.Close() }, nil
}
