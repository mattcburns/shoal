package cli

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/mattcburns/shoal/internal/common/config"
	"github.com/mattcburns/shoal/internal/common/directory"
	"github.com/mattcburns/shoal/internal/common/netbox"
)

// relativeDefaultDeviceStoreDir is the last-resort fallback default when
// SHOAL_DEVICE_STORE_DIR is unset and $HOME can't be determined either.
const relativeDefaultDeviceStoreDir = "./data/devices"

// defaultDeviceStoreDir picks the directory used when neither NetBox nor
// SHOAL_DEVICE_STORE_DIR is configured, so `shoal serve` and the one-shot
// `shoal deploy *`/`shoal discover *` CLI invocations have a working device
// directory out of the box. It prefers an absolute path under $HOME (like
// SerialSSHKey's own default in config.Load) rather than a bare relative
// "./data/devices": the long-running server and separate CLI invocations
// are commonly launched from different working directories, and a
// cwd-relative default would silently give each of them its own directory
// -- defeating the point of an always-on, shared device directory. Falling
// back to $HOME still isn't a substitute for setting SHOAL_DEVICE_STORE_DIR
// explicitly in any deployment where `shoal serve` and the CLI run as
// different users or on different hosts.
func defaultDeviceStoreDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".shoal", "devices")
	}
	return relativeDefaultDeviceStoreDir
}

// buildDirectory is the single composition-root call that decides which
// directory.Store backend backs device lookups/writes across all of
// cmdServe (cli.go), the deploy subcommands (deploy.go), and discover.go.
// It always returns a non-nil, working Store: the NetBox adapter when
// SHOAL_NETBOX_URL/SHOAL_NETBOX_TOKEN are both set, otherwise the local
// FileStore rooted at SHOAL_DEVICE_STORE_DIR (or defaultDeviceStoreDir).
// Since directory.Store is signature-identical to netbox.API /
// netbox.LifecycleWriter / netbox.DeviceResolver, the returned value can be
// passed directly into any existing field/parameter typed with those
// narrower interfaces -- no changes needed in internal/discover or
// internal/deploy/job.
func buildDirectory(cfg config.Config, log *slog.Logger) (directory.Store, error) {
	if cfg.NetBoxURL != "" && cfg.NetBoxToken != "" {
		return netbox.New(cfg.NetBoxURL, cfg.NetBoxToken), nil
	}
	dir := cfg.DeviceStoreDir
	if dir == "" {
		dir = defaultDeviceStoreDir()
	}
	st, err := directory.NewFileStore(dir)
	if err != nil {
		return nil, fmt.Errorf("device store: %w", err)
	}
	if log != nil {
		log.Info("local device store enabled", "dir", dir)
	}
	return st, nil
}

// credentialsNB returns the deviceNB-shaped view (see credentials.go) of
// dirStore to wire into deviceCreds -- but only when NetBox is actually
// configured.
//
// deviceCreds.Put treats a lookup() error as "device not found" and rejects
// the request outright whenever nb is non-nil, by design: a NetBox-backed
// install shouldn't silently create BMC credentials for a device NetBox has
// never heard of. Before this unit, nb was nil whenever NetBox wasn't
// configured, so any device id could have its first credential set here --
// the common "set BMC creds before ever running discover" lab flow. Wiring
// the local FileStore in unconditionally as nb would flip that: PUT
// credentials for a device not yet present in the FileStore would start
// failing with "device not found" purely because the always-on local
// fallback made nb non-nil. So the FileStore is deliberately NOT used for
// this one call site; nb stays nil unless NetBox is configured, preserving
// prior behavior exactly. (dirStore is still used everywhere else --
// discover ingest/confirm upsert, and job.Options.NetBox/DeviceResolver --
// where the real backend being always-present is exactly the point.)
func credentialsNB(cfg config.Config, dirStore directory.Store) deviceNB {
	if cfg.NetBoxURL == "" || cfg.NetBoxToken == "" || dirStore == nil {
		return nil
	}
	if nb, ok := dirStore.(deviceNB); ok {
		return nb
	}
	return nil
}
