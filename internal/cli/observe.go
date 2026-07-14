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
	"github.com/mattcburns/shoal/internal/core/ai"
	"github.com/mattcburns/shoal/internal/core/reconcile"
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
	events := fs.Int("events", 0, "include last N telemetry events (0=status only)")
	bmcURL := fs.String("bmc-url", "", "optional Redfish base URL for power state")
	bmcUser := fs.String("bmc-user", cfg.BMCUsername, "BMC username")
	bmcPass := fs.String("bmc-pass", cfg.BMCPassword, "BMC password")
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
		log.Warn("job store unavailable", "err", err.Error())
	} else if closer != nil {
		defer closer()
	}

	var telem telemetry.Store
	if cfg.TelemetryDatabaseURL != "" {
		db, err := telemetry.OpenAndMigrate(ctx, cfg.TelemetryDatabaseURL)
		if err != nil {
			log.Warn("telemetry store unavailable", "err", err.Error())
		} else {
			defer db.Close()
			telem = telemetry.NewPostgres(db)
		}
	}
	if telem == nil {
		telem = telemetry.NewMemory()
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
		if err == nil {
			if err := bmc.Open(ctx); err == nil {
				st, _ = svc.StatusWithPower(ctx, *deviceID, bmc, "")
				_ = bmc.Close(context.Background())
			}
		}
	}

	out := map[string]any{"status": st}
	if *events > 0 && telem != nil {
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

	var telem telemetry.Store
	if cfg.TelemetryDatabaseURL != "" {
		db, err := telemetry.OpenAndMigrate(ctx, cfg.TelemetryDatabaseURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "telemetry: %v\n", err)
			return 1
		}
		defer db.Close()
		telem = telemetry.NewPostgres(db)
	} else {
		telem = telemetry.NewMemory()
		log.Warn("no SHOAL_TELEMETRY_DATABASE_URL; using memory store (results not durable)")
	}

	var rec reconcile.Reconciler
	if llm, err := ai.NewFromConfig(cfg); err == nil && llm != nil {
		if r, err := reconcile.New(llm, log); err == nil {
			rec = r
		}
	}
	// Deterministic-only reconciler when AI unavailable.
	if rec == nil {
		if r, err := reconcile.New(&ai.Fake{Content: `{}`}, log); err == nil {
			rec = r
		}
	}

	p := poll.New(log, telem, redfish.NewBMC)
	p.Events = rec
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
	if err != nil {
		fmt.Fprintf(os.Stderr, "poll: %v\n", err)
		return 1
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"device_id":       *deviceID,
		"sel_new":         selN,
		"sensors_written": sensN,
	})
	return 0
}
