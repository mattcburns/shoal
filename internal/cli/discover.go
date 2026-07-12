package cli

import (
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mattcburns/shoal/internal/common/config"
	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/netbox"
	"github.com/mattcburns/shoal/internal/core/ai"
	"github.com/mattcburns/shoal/internal/core/reconcile"
	"github.com/mattcburns/shoal/internal/discover"
)

func cmdDiscover(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: shoal discover <ingest> [flags]")
		return 2
	}
	switch args[0] {
	case "ingest":
		return cmdDiscoverIngest(args[1:])
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

	llm, err := ai.NewFromConfig(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai: %v\n", err)
		return 1
	}
	var rec reconcile.Reconciler
	if llm != nil {
		rec, err = reconcile.New(llm, log)
		if err != nil {
			fmt.Fprintf(os.Stderr, "reconcile: %v\n", err)
			return 1
		}
	}
	var nb netbox.API
	if cfg.NetBoxURL != "" && cfg.NetBoxToken != "" {
		nb = netbox.New(cfg.NetBoxURL, cfg.NetBoxToken)
	} else {
		log.Warn("netbox not configured; using memory store")
		nb = netbox.NewMemory()
	}
	svc := discover.New(log, rec, openSecrets(cfg), nb)
	got, err := svc.Ingest(context.Background(), in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ingest: %v\n", err)
		return 1
	}
	_ = json.NewEncoder(os.Stdout).Encode(got)
	return 0
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
