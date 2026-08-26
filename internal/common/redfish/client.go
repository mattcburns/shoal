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

// SetBootOverrideOnceCD sets one-time CD/virtual-CD boot (idempotent).
// Dell iDRAC virtual media is typically UsbCd, not physical Cd; sushy uses Cd.
func (c *client) SetBootOverrideOnceCD(ctx context.Context, systemID string) error {
	sys, err := c.computerSystem(systemID)
	if err != nil {
		return err
	}
	cur, err := c.GetBoot(ctx, systemID)
	if err != nil {
		return err
	}
	vendor := detectVendor(sys.Manufacturer, sys.Model)
	// Dell first: stage one-time virtual-CD boot through the iDRAC's own OEM
	// attributes instead of the standard Boot PATCH. A raw SOL capture on a
	// live R750 (iDRAC9) showed the standard override never actually winning:
	// UsbCd is absent from this iDRAC's AllowableValues, the Cd PATCH reads
	// back Disabled/None, and the host walks its normal boot order (disk →
	// "Boot Failed: Ubuntu" → PXE → virtual media last). Every apparent
	// success was the boot order falling through to the virtual optical after
	// PXE failed *quickly*; every unexplained stall was PXE retrying slowly.
	// The Boot PATCH also queues a Lifecycle Controller "BIOS.Setup.1-1"
	// config job that runs during POST with console redirection disabled,
	// adding minutes of marker-less boot time per job. ServerBoot.1 is
	// iDRAC-native (no LC job) and targets the virtual CD explicitly.
	if vendor == VendorDell {
		if err := c.dellOneTimeVirtualCD(); err == nil {
			return nil
		}
	}
	primary := gofishredfish.CdBootSourceOverrideTarget
	if vendor == VendorDell {
		primary = gofishredfish.BootSourceOverrideTarget("UsbCd")
	}
	if cur.OverrideEnabled == string(gofishredfish.OnceBootSourceOverrideEnabled) &&
		(cur.OverrideTarget == string(primary) || cur.OverrideTarget == string(gofishredfish.CdBootSourceOverrideTarget) ||
			cur.OverrideTarget == "UsbCd") {
		return nil
	}
	boot := sys.Boot
	boot.BootSourceOverrideEnabled = gofishredfish.OnceBootSourceOverrideEnabled
	boot.BootSourceOverrideTarget = primary
	if err := sys.SetBoot(boot); err != nil {
		if primary != gofishredfish.CdBootSourceOverrideTarget {
			boot.BootSourceOverrideTarget = gofishredfish.CdBootSourceOverrideTarget
			if err2 := sys.SetBoot(boot); err2 == nil {
				return nil
			}
		}
		return fmt.Errorf("redfish: set boot override: %w", err)
	}
	return nil
}

// dellOneTimeVirtualCD stages a one-time boot from the iDRAC virtual CD/DVD
// via Dell's OEM manager attributes (ServerBoot.1.FirstBootDevice=VCD-DVD,
// BootOnce=Enabled) -- the racadm-equivalent mechanism the iDRAC applies
// itself on the next power cycle, with no BIOS Lifecycle Controller job.
// Tries each manager's DellAttributes then plain Attributes endpoint;
// returns nil on the first accepted PATCH.
func (c *client) dellOneTimeVirtualCD() error {
	api, err := c.apiClient()
	if err != nil {
		return err
	}
	managers, err := api.Service.Managers()
	if err != nil {
		return fmt.Errorf("redfish: managers: %w", err)
	}
	payload := map[string]any{
		"Attributes": map[string]any{
			"ServerBoot.1.BootOnce":        "Enabled",
			"ServerBoot.1.FirstBootDevice": "VCD-DVD",
		},
	}
	var firstErr error
	for _, m := range managers {
		if m == nil {
			continue
		}
		base := strings.TrimSuffix(m.ODataID, "/")
		for _, u := range []string{
			base + "/Oem/Dell/DellAttributes/" + m.ID,
			base + "/Attributes",
		} {
			resp, perr := api.Patch(u, payload)
			if perr != nil {
				if firstErr == nil {
					firstErr = perr
				}
				continue
			}
			code := resp.StatusCode
			_ = resp.Body.Close()
			if code >= 200 && code < 300 {
				return nil
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("redfish: dell ServerBoot PATCH %s: HTTP %d", u, code)
			}
		}
	}
	if firstErr == nil {
		firstErr = fmt.Errorf("redfish: dell ServerBoot: no manager accepted PATCH")
	}
	return firstErr
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
// When systemID is set, only that system's media is returned (or its matching
// manager). Merging every manager's slots would attach media to the wrong node
// in multi-BMC labs (sushy-tools exposes one Cd per system/manager).
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
		// If the system exposes media, do not merge other systems' managers.
		if len(out) > 0 {
			return out, nil
		}
	}

	// Fallback: managers. When systemID is known, only the manager with the
	// same id/uuid (sushy-tools 1:1 mapping) is used.
	managers, err := api.Service.Managers()
	if err == nil {
		for _, m := range managers {
			if systemID != "" && m.ID != systemID && m.UUID != systemID {
				// Still allow empty systemID (list all) for diagnostics.
				continue
			}
			vms, vmErr := m.VirtualMedia()
			if vmErr != nil {
				continue
			}
			for _, vm := range vms {
				mapped := mapVM(vm)
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

	// Last resort: if systemID was empty or no manager matched, scan all managers.
	if len(out) == 0 && systemID == "" {
		managers, err = api.Service.Managers()
		if err == nil {
			for _, m := range managers {
				vms, vmErr := m.VirtualMedia()
				if vmErr != nil {
					continue
				}
				for _, vm := range vms {
					out = append(out, mapVM(vm))
				}
			}
		}
	}

	// Empty is valid: sushy-tools eject removes the libvirt CD device entirely,
	// so a successful cleanup leaves zero slots until the next InsertMedia path
	// recreates them. Callers that need a tray for insert must check len==0.
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

// InsertVirtualMedia inserts an image URL, always ejecting first if already
// inserted -- including when the reported Image URL already matches. A BMC's
// virtual-media HTTP redirection session can go stale between jobs (e.g. the
// serving process on the operator side restarted, or the BMC's own session
// timed out) while Redfish still reports Inserted=true with the same URL;
// skipping the eject/reinsert in that case silently boots a dead media mount
// -- the boot override gets consumed (BIOS attempts the CD) but the guest
// never actually reads anything, so no SHOAL| markers ever appear. Confirmed
// live: a job stalled with zero markers for the full 15-minute window
// immediately after a prior job on this device left the same iso_url
// "Inserted", while a fresh eject+insert with an unchanged BMC/ISO/network
// path had succeeded minutes earlier. Always eject-then-reinsert instead.
func (c *client) InsertVirtualMedia(_ context.Context, mediaURI, imageURL string) error {
	vm, err := c.virtualMediaByURI(mediaURI)
	if err != nil {
		return err
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

// Power performs a reset action. On when already On becomes ForceRestart so
// one-time virtual media is observed (Deploy). Operator power control uses Reset.
func (c *client) Power(ctx context.Context, systemID, resetType string) error {
	info, err := c.GetSystem(ctx, systemID)
	if err != nil {
		return err
	}
	if resetType == "On" && strings.EqualFold(info.PowerState, "On") {
		resetType = "ForceRestart"
	}
	return c.Reset(ctx, systemID, resetType)
}

// Reset applies the named Redfish reset type with no rewriting.
func (c *client) Reset(ctx context.Context, systemID, resetType string) error {
	_ = ctx
	sys, err := c.computerSystem(systemID)
	if err != nil {
		return err
	}
	if err := sys.Reset(gofishredfish.ResetType(resetType)); err != nil {
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
