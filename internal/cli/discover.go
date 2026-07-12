package cli

import (
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/mattcburns/shoal/internal/common/config"
	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/netbox"
	"github.com/mattcburns/shoal/internal/core/ai"
	"github.com/mattcburns/shoal/internal/core/fewshot"
	"github.com/mattcburns/shoal/internal/core/reconcile"
	"github.com/mattcburns/shoal/internal/discover"
)

func cmdDiscover(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: shoal discover <ingest|confirm> [flags]")
		return 2
	}
	switch args[0] {
	case "ingest":
		return cmdDiscoverIngest(args[1:])
	case "confirm":
		return cmdDiscoverConfirm(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown discover subcommand %q\n", args[0])
		return 2
	}
}

func cmdDiscoverIngest(args []string) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}
	log := newLogger(cfg.LogLevel)

	fs := flag.NewFlagSet("discover ingest", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	kind := fs.String("kind", "", "redfish_json | csv | photo")
	file := fs.String("file", "", "input file path")
	bmcIP := fs.String("bmc-ip", "", "BMC IP (optional operator hint)")
	bmcUser := fs.String("bmc-user", cfg.BMCUsername, "BMC username to stash")
	bmcPass := fs.String("bmc-pass", cfg.BMCPassword, "BMC password to stash (never logged)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *kind == "" || *file == "" {
		fmt.Fprintln(os.Stderr, "discover ingest requires -kind and -file")
		return 2
	}

	raw, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read: %v\n", err)
		return 1
	}

	in := models.RawAssetInput{
		Kind:        *kind,
		BMCIP:       *bmcIP,
		BMCUsername: *bmcUser,
		BMCPassword: *bmcPass,
	}
	switch strings.ToLower(*kind) {
	case "redfish_json":
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			fmt.Fprintf(os.Stderr, "json: %v\n", err)
			return 1
		}
		in.RedfishJSON = m
	case "csv":
		row, err := parseCSVRow(raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "csv: %v\n", err)
			return 1
		}
		in.CSVRow = row
	case "photo":
		in.PhotoBase64 = base64.StdEncoding.EncodeToString(raw)
	default:
		fmt.Fprintf(os.Stderr, "unknown kind %q\n", *kind)
		return 2
	}

	svc, err := openDiscoverService(cfg, log)
	if err != nil {
		fmt.Fprintf(os.Stderr, "discover: %v\n", err)
		return 1
	}
	got, err := svc.Ingest(context.Background(), in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ingest: %v\n", err)
		return 1
	}
	_ = json.NewEncoder(os.Stdout).Encode(got)
	return 0
}

func cmdDiscoverConfirm(args []string) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}
	log := newLogger(cfg.LogLevel)

	fs := flag.NewFlagSet("discover confirm", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	// Prefer a single JSON file: {"kind","input","result",...}
	file := fs.String("file", "", "JSON ConfirmRequest file")
	// Or split input + result files
	kind := fs.String("kind", "", "redfish_json | csv | photo (with -input and -result)")
	inputPath := fs.String("input", "", "JSON map of redacted input")
	resultPath := fs.String("result", "", "JSON NormalizationResult (or full IngestResult)")
	deviceID := fs.String("device-id", "", "optional correlation id")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	var req discover.ConfirmRequest
	if *file != "" {
		raw, err := os.ReadFile(*file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read: %v\n", err)
			return 1
		}
		if err := json.Unmarshal(raw, &req); err != nil {
			fmt.Fprintf(os.Stderr, "json: %v\n", err)
			return 1
		}
	} else if *kind != "" && *inputPath != "" && *resultPath != "" {
		req.Kind = *kind
		inRaw, err := os.ReadFile(*inputPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "input: %v\n", err)
			return 1
		}
		if err := json.Unmarshal(inRaw, &req.Input); err != nil {
			fmt.Fprintf(os.Stderr, "input json: %v\n", err)
			return 1
		}
		resRaw, err := os.ReadFile(*resultPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "result: %v\n", err)
			return 1
		}
		// Accept either NormalizationResult or IngestResult wrapper.
		var wrap struct {
			models.NormalizationResult
			Asset       models.NormalizedAsset      `json:"asset"`
			Confidences []models.FieldConfidence    `json:"confidences"`
			NeedsReview bool                        `json:"needs_review"`
			Result      *models.NormalizationResult `json:"result"`
		}
		if err := json.Unmarshal(resRaw, &wrap); err != nil {
			fmt.Fprintf(os.Stderr, "result json: %v\n", err)
			return 1
		}
		if wrap.Result != nil {
			req.Result = *wrap.Result
		} else if wrap.Asset.Serial != "" {
			req.Result = models.NormalizationResult{
				Asset:       wrap.Asset,
				Confidences: wrap.Confidences,
				NeedsReview: wrap.NeedsReview,
			}
		} else {
			req.Result = wrap.NormalizationResult
		}
	} else {
		fmt.Fprintln(os.Stderr, "discover confirm requires -file OR (-kind -input -result)")
		return 2
	}
	if *deviceID != "" {
		req.DeviceID = *deviceID
	}

	svc, err := openDiscoverService(cfg, log)
	if err != nil {
		fmt.Fprintf(os.Stderr, "discover: %v\n", err)
		return 1
	}
	got, err := svc.Confirm(context.Background(), req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "confirm: %v\n", err)
		return 1
	}
	_ = json.NewEncoder(os.Stdout).Encode(got)
	return 0
}

func openDiscoverService(cfg config.Config, log *slog.Logger) (*discover.Service, error) {
	if log == nil {
		log = newLogger(cfg.LogLevel)
	}
	var fsStore fewshot.Store
	if cfg.FewShotDir != "" {
		st, err := fewshot.NewFileStore(cfg.FewShotDir)
		if err != nil {
			return nil, fmt.Errorf("fewshot: %w", err)
		}
		fsStore = st
	}

	llm, err := ai.NewFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	var rec reconcile.Reconciler
	if llm != nil {
		rec, err = reconcile.NewWithFewShot(llm, log, fsStore)
		if err != nil {
			return nil, err
		}
	}
	var nb netbox.API
	if cfg.NetBoxURL != "" && cfg.NetBoxToken != "" {
		nb = netbox.New(cfg.NetBoxURL, cfg.NetBoxToken)
	} else {
		log.Warn("netbox not configured; using memory store")
		nb = netbox.NewMemory()
	}
	return discover.NewWithFewShot(log, rec, openSecrets(cfg), nb, fsStore), nil
}

func parseCSVRow(raw []byte) (map[string]string, error) {
	r := csv.NewReader(strings.NewReader(string(raw)))
	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) < 2 {
		// single header=value lines? or one data row with header
		if len(records) == 1 && len(records[0]) > 0 && strings.Contains(records[0][0], "=") {
			out := map[string]string{}
			for _, cell := range records[0] {
				k, v, ok := strings.Cut(cell, "=")
				if ok {
					out[strings.TrimSpace(k)] = strings.TrimSpace(v)
				}
			}
			return out, nil
		}
		return nil, fmt.Errorf("need header + one data row")
	}
	headers := records[0]
	row := records[1]
	out := map[string]string{}
	for i, h := range headers {
		if i < len(row) {
			out[strings.TrimSpace(h)] = strings.TrimSpace(row[i])
		}
	}
	return out, nil
}
