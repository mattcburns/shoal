package redfish

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/stmcginnis/gofish"
	gofishredfish "github.com/stmcginnis/gofish/redfish"
)

// client is the gofish-backed BMC implementation.
type client struct {
	cfg    Config
	mu     sync.Mutex
	api    *gofish.APIClient
	opened bool
}

// NewBMC constructs the gofish-backed implementation. Call Open before use.
func NewBMC(cfg Config) (BMC, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("redfish: empty BaseURL")
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 1
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 30 * time.Second
	}
	if cfg.AuthMode == "" {
		cfg.AuthMode = "basic"
	}
	if cfg.TLSMode == "" {
		cfg.TLSMode = "off"
	}
	return &client{cfg: cfg}, nil
}

// Open connects to the Redfish service root.
func (c *client) Open(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.opened && c.api != nil {
		return nil
	}

	httpClient, err := c.httpClient()
	if err != nil {
		return err
	}
	httpClient.Timeout = c.cfg.RequestTimeout

	gcfg := gofish.ClientConfig{
		Endpoint:              c.cfg.BaseURL,
		Username:              c.cfg.Username,
		Password:              c.cfg.Password,
		HTTPClient:            httpClient,
		BasicAuth:             strings.EqualFold(c.cfg.AuthMode, "basic"),
		Insecure:              strings.EqualFold(c.cfg.TLSMode, "insecure"),
		MaxConcurrentRequests: int64(c.cfg.MaxConcurrent),
		ReuseConnections:      true,
	}

	api, err := gofish.ConnectContext(ctx, gcfg)
	if err != nil {
		return fmt.Errorf("redfish: connect: %w", err)
	}
	c.api = api
	c.opened = true
	return nil
}

func (c *client) httpClient() (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	switch strings.ToLower(c.cfg.TLSMode) {
	case "off":
		// plain HTTP endpoints; TLS config unused for http://
	case "insecure":
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // explicit lab/BMC self-signed mode
	case "custom_ca":
		pem, err := os.ReadFile(c.cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("redfish: read CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("redfish: no certificates in CA file")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	default:
		return nil, fmt.Errorf("redfish: unknown TLS mode %q", c.cfg.TLSMode)
	}
	return &http.Client{Transport: transport}, nil
}

// Close logs out and releases the client.
func (c *client) Close(_ context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.api != nil {
		c.api.Logout()
		c.api = nil
	}
	c.opened = false
	return nil
}

func (c *client) apiClient() (*gofish.APIClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.opened || c.api == nil {
		return nil, fmt.Errorf("redfish: not open")
	}
	return c.api, nil
}

// ServiceRoot returns service root metadata.
func (c *client) ServiceRoot(_ context.Context) (ServiceRoot, error) {
	api, err := c.apiClient()
	if err != nil {
		return ServiceRoot{}, err
	}
	sr := api.Service
	if sr == nil {
		return ServiceRoot{}, fmt.Errorf("redfish: nil service root")
	}
	return ServiceRoot{
		Name:           sr.Name,
		RedfishVersion: sr.RedfishVersion,
		UUID:           sr.UUID,
	}, nil
}

// ListSystems returns all computer systems.
func (c *client) ListSystems(_ context.Context) ([]SystemInfo, error) {
	api, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	systems, err := api.Service.Systems()
	if err != nil {
		return nil, fmt.Errorf("redfish: systems: %w", err)
	}
	out := make([]SystemInfo, 0, len(systems))
	for _, s := range systems {
		out = append(out, mapSystem(s))
	}
	return out, nil
}

// GetSystem returns one system by ID or Name (or first if systemID empty and only one exists).
func (c *client) GetSystem(ctx context.Context, systemID string) (SystemInfo, error) {
	systems, err := c.ListSystems(ctx)
	if err != nil {
		return SystemInfo{}, err
	}
	if systemID == "" {
		if len(systems) == 0 {
			return SystemInfo{}, fmt.Errorf("redfish: no systems")
		}
		if len(systems) > 1 {
			return SystemInfo{}, fmt.Errorf("redfish: multiple systems; specify system id or name")
		}
		return systems[0], nil
	}
	for _, s := range systems {
		if s.ID == systemID || s.Name == systemID || strings.HasSuffix(s.ODataID, "/"+systemID) {
			return s, nil
		}
	}
	return SystemInfo{}, fmt.Errorf("redfish: system %q not found", systemID)
}

func mapSystem(s *gofishredfish.ComputerSystem) SystemInfo {
	return SystemInfo{
		ID:           s.ID,
		Name:         s.Name,
		UUID:         s.UUID,
		Serial:       s.SerialNumber,
		Model:        s.Model,
		Manufacturer: s.Manufacturer,
		PowerState:   string(s.PowerState),
		ODataID:      s.ODataID,
	}
}

func (c *client) computerSystem(systemID string) (*gofishredfish.ComputerSystem, error) {
	api, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	systems, err := api.Service.Systems()
	if err != nil {
		return nil, fmt.Errorf("redfish: systems: %w", err)
	}
	if systemID == "" {
		if len(systems) == 0 {
			return nil, fmt.Errorf("redfish: no systems")
		}
		if len(systems) > 1 {
			return nil, fmt.Errorf("redfish: multiple systems; specify system id or name")
		}
		return systems[0], nil
	}
	for _, s := range systems {
		if s.ID == systemID || s.Name == systemID || strings.HasSuffix(s.ODataID, "/"+systemID) {
			return s, nil
		}
	}
	return nil, fmt.Errorf("redfish: system %q not found", systemID)
}

// GetBoot returns boot override state.
func (c *client) GetBoot(_ context.Context, systemID string) (BootInfo, error) {
	sys, err := c.computerSystem(systemID)
	if err != nil {
		return BootInfo{}, err
	}
	return BootInfo{
		OverrideEnabled: string(sys.Boot.BootSourceOverrideEnabled),
		OverrideTarget:  string(sys.Boot.BootSourceOverrideTarget),
	}, nil
}

// SetBootOverrideOnceCD sets one-time CD boot (idempotent).
func (c *client) SetBootOverrideOnceCD(ctx context.Context, systemID string) error {
	sys, err := c.computerSystem(systemID)
	if err != nil {
		return err
	}
	cur, err := c.GetBoot(ctx, systemID)
	if err != nil {
		return err
	}
	if cur.OverrideEnabled == string(gofishredfish.OnceBootSourceOverrideEnabled) &&
		cur.OverrideTarget == string(gofishredfish.CdBootSourceOverrideTarget) {
		return nil
	}
	boot := sys.Boot
	boot.BootSourceOverrideEnabled = gofishredfish.OnceBootSourceOverrideEnabled
	boot.BootSourceOverrideTarget = gofishredfish.CdBootSourceOverrideTarget
	if err := sys.SetBoot(boot); err != nil {
		return fmt.Errorf("redfish: set boot override: %w", err)
	}
	return nil
}

// ClearBootOverride disables boot override (idempotent).
//
// sushy-tools maps BootSourceOverrideTarget onto libvirt boot devices and
// rejects target "None" ("Target libvirt device None does not exist"). Prefer
// Disabled + Hdd as the normal post-provision boot target for lab emulators.
func (c *client) ClearBootOverride(ctx context.Context, systemID string) error {
	sys, err := c.computerSystem(systemID)
	if err != nil {
		return err
	}
	cur, err := c.GetBoot(ctx, systemID)
	if err != nil {
		return err
	}
	if (cur.OverrideEnabled == string(gofishredfish.DisabledBootSourceOverrideEnabled) ||
		cur.OverrideEnabled == "" || cur.OverrideEnabled == "Disabled") &&
		(cur.OverrideTarget == string(gofishredfish.HddBootSourceOverrideTarget) ||
			cur.OverrideTarget == "Hdd" || cur.OverrideTarget == "") {
		return nil
	}
	boot := sys.Boot
	boot.BootSourceOverrideEnabled = gofishredfish.DisabledBootSourceOverrideEnabled
	boot.BootSourceOverrideTarget = gofishredfish.HddBootSourceOverrideTarget
	if err := sys.SetBoot(boot); err != nil {
		// Retry with Continuous/Hdd if Disabled is rejected by some firmwares.
		boot.BootSourceOverrideEnabled = gofishredfish.ContinuousBootSourceOverrideEnabled
		boot.BootSourceOverrideTarget = gofishredfish.HddBootSourceOverrideTarget
		if err2 := sys.SetBoot(boot); err2 != nil {
			return fmt.Errorf("redfish: clear boot override: %w", err)
		}
	}
	return nil
}

// ListVirtualMedia lists virtual media for a system, falling back to managers.
func (c *client) ListVirtualMedia(_ context.Context, systemID string) ([]VirtualMedia, error) {
	api, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	var out []VirtualMedia

	// Prefer system-attached virtual media.
	sys, err := c.computerSystem(systemID)
	if err == nil {
		vms, vmErr := sys.VirtualMedia()
		if vmErr == nil {
			for _, vm := range vms {
				out = append(out, mapVM(vm))
			}
		}
	}

	// Also check managers (sushy-tools often exposes VM here).
	managers, err := api.Service.Managers()
	if err == nil {
		for _, m := range managers {
			vms, vmErr := m.VirtualMedia()
			if vmErr != nil {
				continue
			}
			for _, vm := range vms {
				mapped := mapVM(vm)
				// de-dupe by URI
				dup := false
				for _, existing := range out {
					if existing.URI == mapped.URI {
						dup = true
						break
					}
				}
				if !dup {
					out = append(out, mapped)
				}
			}
		}
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("redfish: no virtual media found")
	}
	return out, nil
}

func mapVM(vm *gofishredfish.VirtualMedia) VirtualMedia {
	m := VirtualMedia{
		URI:      vm.ODataID,
		Name:     vm.Name,
		ID:       vm.ID,
		Image:    vm.Image,
		Inserted: vm.Inserted,
	}
	for _, t := range vm.MediaTypes {
		m.MediaTypes = append(m.MediaTypes, string(t))
		if t == gofishredfish.CDMediaType || t == gofishredfish.DVDMediaType {
			m.SupportsCD = true
		}
	}
	if len(vm.MediaTypes) == 0 {
		// assume CD-capable when types omitted (some emulators)
		m.SupportsCD = true
	}
	return m
}

func (c *client) virtualMediaByURI(mediaURI string) (*gofishredfish.VirtualMedia, error) {
	api, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	// Search systems
	systems, err := api.Service.Systems()
	if err == nil {
		for _, s := range systems {
			vms, vmErr := s.VirtualMedia()
			if vmErr != nil {
				continue
			}
			for _, vm := range vms {
				if vm.ODataID == mediaURI || vm.ID == mediaURI {
					return vm, nil
				}
			}
		}
	}
	managers, err := api.Service.Managers()
	if err == nil {
		for _, m := range managers {
			vms, vmErr := m.VirtualMedia()
			if vmErr != nil {
				continue
			}
			for _, vm := range vms {
				if vm.ODataID == mediaURI || vm.ID == mediaURI {
					return vm, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("redfish: virtual media %q not found", mediaURI)
}

// InsertVirtualMedia inserts an image URL (idempotent if already inserted with same image).
func (c *client) InsertVirtualMedia(_ context.Context, mediaURI, imageURL string) error {
	vm, err := c.virtualMediaByURI(mediaURI)
	if err != nil {
		return err
	}
	if vm.Inserted && vm.Image == imageURL {
		return nil
	}
	if vm.Inserted {
		if err := vm.EjectMedia(); err != nil {
			return fmt.Errorf("redfish: eject before insert: %w", err)
		}
	}
	if err := vm.InsertMedia(imageURL, true, true); err != nil {
		return fmt.Errorf("redfish: insert media: %w", err)
	}
	return nil
}

// EjectVirtualMedia ejects media (idempotent if already empty).
func (c *client) EjectVirtualMedia(_ context.Context, mediaURI string) error {
	vm, err := c.virtualMediaByURI(mediaURI)
	if err != nil {
		return err
	}
	if !vm.Inserted {
		return nil
	}
	if err := vm.EjectMedia(); err != nil {
		return fmt.Errorf("redfish: eject media: %w", err)
	}
	return nil
}

// Power performs a reset action (idempotent for On when already on).
func (c *client) Power(ctx context.Context, systemID, resetType string) error {
	sys, err := c.computerSystem(systemID)
	if err != nil {
		return err
	}
	info, err := c.GetSystem(ctx, systemID)
	if err != nil {
		return err
	}
	rt := gofishredfish.ResetType(resetType)
	if resetType == "On" && strings.EqualFold(info.PowerState, "On") {
		// Force restart so one-time media is observed.
		rt = gofishredfish.ForceRestartResetType
	}
	if err := sys.Reset(rt); err != nil {
		return fmt.Errorf("redfish: power %s: %w", resetType, err)
	}
	return nil
}

// CleanupMediaAndBoot ejects all inserted media and clears boot override.
// Best-effort: continues after individual step failures (powered-off emulators
// may reject some actions).
func (c *client) CleanupMediaAndBoot(ctx context.Context, systemID string) error {
	var firstErr error
	vms, err := c.ListVirtualMedia(ctx, systemID)
	if err != nil {
		firstErr = err
	} else {
		for _, vm := range vms {
			if !vm.Inserted {
				continue
			}
			if err := c.EjectVirtualMedia(ctx, vm.URI); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	if err := c.ClearBootOverride(ctx, systemID); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// Ensure client implements BMC.
var _ BMC = (*client)(nil)
