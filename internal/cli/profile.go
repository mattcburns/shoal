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
	"github.com/mattcburns/shoal/internal/core/ai"
	"github.com/mattcburns/shoal/internal/core/profile"
)

func cmdProfile(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: shoal profile <generate|show|list|approve|save> [flags]")
		return 2
	}
	switch args[0] {
	case "generate":
		return cmdProfileGenerate(args[1:])
	case "show":
		return cmdProfileShow(args[1:])
	case "list":
		return cmdProfileList(args[1:])
	case "approve":
		return cmdProfileApprove(args[1:])
	case "save":
		return cmdProfileSave(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown profile subcommand %q\n", args[0])
		return 2
	}
}

func openProfileStore(cfg config.Config, require bool) (profile.Store, error) {
	if cfg.ProfileDir == "" {
		if require {
			return nil, fmt.Errorf("SHOAL_PROFILE_DIR is required")
		}
		return nil, nil
	}
	return profile.NewFileStore(cfg.ProfileDir)
}

func cmdProfileGenerate(args []string) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}
	fs := flag.NewFlagSet("profile generate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	osFamily := fs.String("os-family", "ubuntu", "OS family")
	osVersion := fs.String("os-version", "", "OS version")
	hostname := fs.String("hostname", "", "target hostname")
	allowDestruct := fs.Bool("allow-destruct", false, "allow destructive steps in generated profile")
	serial := fs.String("serial", "unknown", "asset serial (identity only)")
	bmcIP := fs.String("bmc-ip", "", "asset BMC IP")
	vendor := fs.String("vendor", "", "asset vendor")
	model := fs.String("model", "", "asset model")
	save := fs.Bool("save", false, "write to SHOAL_PROFILE_DIR")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	log := newLogger(cfg.LogLevel)
	llm, err := ai.NewFromConfig(cfg)
	if err != nil || llm == nil {
		fmt.Fprintf(os.Stderr, "profile generate: AI required (set SHOAL_AI_PROVIDER / model): %v\n", err)
		return 1
	}
	svc, err := profile.New(llm, log)
	if err != nil {
		fmt.Fprintf(os.Stderr, "profile: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	p, err := svc.GenerateProvisioningProfile(ctx,
		models.NormalizedAsset{
			Serial: *serial, BMCIP: *bmcIP, Vendor: *vendor, Model: *model,
		},
		models.ProfileRequirements{
			OSFamily: *osFamily, OSVersion: *osVersion, Hostname: *hostname,
			AllowDestruct: *allowDestruct,
		},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate: %v\n", err)
		return 1
	}
	if *save {
		st, err := openProfileStore(cfg, true)
		if err != nil {
			fmt.Fprintf(os.Stderr, "store: %v\n", err)
			return 1
		}
		rec, err := st.Save(ctx, p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "save: %v\n", err)
			return 1
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rec)
		return 0
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(p)
	return 0
}

func cmdProfileSave(args []string) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}
	fs := flag.NewFlagSet("profile save", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	file := fs.String("file", "", "path to ProvisioningProfile JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" {
		fmt.Fprintln(os.Stderr, "profile save: -file required")
		return 2
	}
	b, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read: %v\n", err)
		return 1
	}
	var p models.ProvisioningProfile
	if err := json.Unmarshal(b, &p); err != nil {
		fmt.Fprintf(os.Stderr, "json: %v\n", err)
		return 1
	}
	if err := profile.ProvisioningProfile(p); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	st, err := openProfileStore(cfg, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "store: %v\n", err)
		return 1
	}
	rec, err := st.Save(context.Background(), p)
	if err != nil {
		fmt.Fprintf(os.Stderr, "save: %v\n", err)
		return 1
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(rec)
	return 0
}

func cmdProfileShow(args []string) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}
	fs := flag.NewFlagSet("profile show", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	ref := fs.String("ref", "", "profile ref")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *ref == "" {
		fmt.Fprintln(os.Stderr, "profile show: -ref required")
		return 2
	}
	st, err := openProfileStore(cfg, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "store: %v\n", err)
		return 1
	}
	rec, err := st.Get(context.Background(), *ref)
	if err != nil {
		fmt.Fprintf(os.Stderr, "get: %v\n", err)
		return 1
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(rec)
	return 0
}

func cmdProfileList(args []string) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}
	st, err := openProfileStore(cfg, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "store: %v\n", err)
		return 1
	}
	list, err := st.List(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "list: %v\n", err)
		return 1
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(list)
	return 0
}

func cmdProfileApprove(args []string) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}
	fs := flag.NewFlagSet("profile approve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	ref := fs.String("ref", "", "profile ref")
	by := fs.String("by", "operator", "approver identity (logged, not a secret)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *ref == "" {
		fmt.Fprintln(os.Stderr, "profile approve: -ref required")
		return 2
	}
	st, err := openProfileStore(cfg, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "store: %v\n", err)
		return 1
	}
	rec, err := st.Approve(context.Background(), *ref, *by)
	if err != nil {
		fmt.Fprintf(os.Stderr, "approve: %v\n", err)
		return 1
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(rec)
	return 0
}
