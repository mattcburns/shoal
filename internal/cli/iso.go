package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mattcburns/shoal/internal/common/config"
	"github.com/mattcburns/shoal/internal/core/profile"
	"github.com/mattcburns/shoal/internal/deploy/iso"
)

func cmdDeployISO(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: shoal deploy iso <build|publish> [flags]")
		return 2
	}
	switch args[0] {
	case "build":
		return cmdISOBuild(args[1:])
	case "publish":
		return cmdISOPublish(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown deploy iso subcommand %q\n", args[0])
		return 2
	}
}

func cmdISOBuild(args []string) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}
	log := newLogger(cfg.LogLevel)
	fs := flag.NewFlagSet("deploy iso build", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	name := fs.String("name", "shoal-marker.iso", "output ISO basename")
	outDir := fs.String("out-dir", "", "local output directory (default: temp or -out parent)")
	out := fs.String("out", "", "full output path (overrides -name/-out-dir)")
	payload := fs.String("payload", "", "non-secret embedded payload text")
	payloadFile := fs.String("payload-file", "", "read non-secret payload from file")
	profileRef := fs.String("profile-ref", "", "load iso_base/name/payload hints from profile store")
	publish := fs.Bool("publish", false, "also publish to SHOAL_ISO_PUBLISH_DIR")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	in := iso.BuildInput{
		Name:       *name,
		OutDir:     *outDir,
		ScriptPath: cfg.ISOBuildScript,
	}
	if *out != "" {
		in.OutDir = filepath.Dir(*out)
		in.Name = filepath.Base(*out)
	}
	if *payloadFile != "" {
		b, err := os.ReadFile(*payloadFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "payload-file: %v\n", err)
			return 1
		}
		in.EmbeddedPayload = string(b)
	} else if *payload != "" {
		in.EmbeddedPayload = *payload
	}
	if *profileRef != "" {
		if cfg.ProfileDir == "" {
			fmt.Fprintln(os.Stderr, "profile-ref requires SHOAL_PROFILE_DIR")
			return 1
		}
		st, err := profile.NewFileStore(cfg.ProfileDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "profile store: %v\n", err)
			return 1
		}
		rec, err := st.Get(context.Background(), *profileRef)
		if err != nil {
			fmt.Fprintf(os.Stderr, "profile: %v\n", err)
			return 1
		}
		if in.Name == "shoal-marker.iso" && rec.Profile.ISOBase != "" {
			base := filepath.Base(rec.Profile.ISOBase)
			if filepath.Ext(base) == "" {
				base += ".iso"
			}
			in.Name = base
		}
		if in.EmbeddedPayload == "" && rec.Profile.EmbeddedPayload != "" {
			in.EmbeddedPayload = rec.Profile.EmbeddedPayload
		}
	}

	b := iso.NewScriptBuilder(cfg.ISOBuildScript, log)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	art, err := b.Build(ctx, in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build: %v\n", err)
		return 1
	}

	result := map[string]any{
		"path": art.Path,
		"name": art.Name,
		"size": art.Size,
	}
	if *publish {
		if cfg.ISOPublishDir == "" || cfg.ISOBaseURL == "" {
			fmt.Fprintln(os.Stderr, "publish requires SHOAL_ISO_PUBLISH_DIR and SHOAL_ISO_BASE_URL")
			return 1
		}
		url, err := b.Publish(ctx, art, iso.PublishDest{Dir: cfg.ISOPublishDir, BaseURL: cfg.ISOBaseURL})
		if err != nil {
			fmt.Fprintf(os.Stderr, "publish: %v\n", err)
			return 1
		}
		result["url"] = url
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(result)
	return 0
}

func cmdISOPublish(args []string) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}
	log := newLogger(cfg.LogLevel)
	fs := flag.NewFlagSet("deploy iso publish", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	file := fs.String("file", "", "path to local .iso")
	name := fs.String("name", "", "published basename (default: source basename)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" {
		fmt.Fprintln(os.Stderr, "deploy iso publish: -file required")
		return 2
	}
	if cfg.ISOPublishDir == "" || cfg.ISOBaseURL == "" {
		fmt.Fprintln(os.Stderr, "publish requires SHOAL_ISO_PUBLISH_DIR and SHOAL_ISO_BASE_URL")
		return 1
	}
	base := *name
	if base == "" {
		base = filepath.Base(*file)
	}
	st, err := os.Stat(*file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stat: %v\n", err)
		return 1
	}
	b := iso.NewScriptBuilder(cfg.ISOBuildScript, log)
	url, err := b.Publish(context.Background(), iso.Artifact{
		Path: *file, Name: base, Size: st.Size(),
	}, iso.PublishDest{Dir: cfg.ISOPublishDir, BaseURL: cfg.ISOBaseURL})
	if err != nil {
		fmt.Fprintf(os.Stderr, "publish: %v\n", err)
		return 1
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]any{"path": filepath.Join(cfg.ISOPublishDir, base), "url": url, "name": base})
	return 0
}
