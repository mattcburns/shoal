package redfish

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Redfish Boot property enum values this package reads/writes (DMTF
// BootSourceOverrideEnabled / BootSourceOverrideTarget). Named to give
// typo-prone bare comparisons/assignments a single, compiler-checked source
// of truth (gofish's equivalent was a distinct Go type per enum).
const (
	bootOverrideOnce       = "Once"
	bootOverrideContinuous = "Continuous"
	bootOverrideDisabled   = "Disabled"

	bootTargetCd    = "Cd"
	bootTargetUsbCd = "UsbCd"
	bootTargetHdd   = "Hdd"
)

// client is the hand-written-HTTP-backed BMC implementation.
type client struct {
	cfg    Config
	mu     sync.Mutex
	api    *rfAPI
	root   rfServiceRoot
	opened bool
}

// NewBMC constructs the hand-written-HTTP-backed implementation. Call Open before use.
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

	basicAuth := strings.EqualFold(c.cfg.AuthMode, "basic")
	api, err := newRFAPI(ctx, httpClient, c.cfg.BaseURL, c.cfg.Username, c.cfg.Password, basicAuth, c.cfg.MaxConcurrent)
	if err != nil {
		return err
	}

	var root rfServiceRoot
	if err := api.getJSON("/redfish/v1/", &root); err != nil {
		return fmt.Errorf("redfish: connect: %w", err)
	}

	// Session auth: gofish established a Redfish session (POST username/
	// password to Links.Sessions, reuse the returned X-Auth-Token) whenever
	// AuthMode wasn't "basic" and a Username was configured; Basic-Auth-only
	// otherwise leaves the connection unauthenticated, same as gofish.
	if !basicAuth && c.cfg.Username != "" {
		if err := api.createSession(root.Links.Sessions.ODataID, c.cfg.Username, c.cfg.Password); err != nil {
			return fmt.Errorf("redfish: connect: %w", err)
		}
	}

	c.api = api
	c.root = root
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

// Close logs out of an active session (best-effort; a no-op for Basic-Auth,
// which has no server-side session to tear down) and releases the client.
func (c *client) Close(_ context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.api != nil {
		c.api.logout()
	}
	c.api = nil
	c.opened = false
	return nil
}

func (c *client) apiClient() (*rfAPI, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.opened || c.api == nil {
		return nil, fmt.Errorf("redfish: not open")
	}
	return c.api, nil
}

// managers lists Redfish Managers from the service root.
func (c *client) managers() ([]*rfManager, error) {
	api, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	return fetchCollection[rfManager](api, c.root.Managers.ODataID)
}

// chassisList lists Redfish Chassis from the service root.
func (c *client) chassisList() ([]*rfChassis, error) {
	api, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	return fetchCollection[rfChassis](api, c.root.Chassis.ODataID)
}

// managerNetworkProtocol fetches a manager's NetworkProtocol resource
// (mirrors gofish's (*Manager).NetworkProtocol(), a live GET of the linked
// resource -- not a field already present on the Manager document).
func (c *client) managerNetworkProtocol(m *rfManager) (*rfNetworkProtocolSettings, error) {
	api, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	return fetchOne[rfNetworkProtocolSettings](api, m.NetworkProtocol.ODataID)
}

// ServiceRoot returns service root metadata.
func (c *client) ServiceRoot(_ context.Context) (ServiceRoot, error) {
	if _, err := c.apiClient(); err != nil {
		return ServiceRoot{}, err
	}
	return ServiceRoot{
		Name:           c.root.Name,
		RedfishVersion: c.root.RedfishVersion,
		UUID:           c.root.UUID,
		SystemsURL:     c.root.Systems.ODataID,
		ManagersURL:    c.root.Managers.ODataID,
	}, nil
}

// ListSystems returns all computer systems.
func (c *client) ListSystems(_ context.Context) ([]SystemInfo, error) {
	api, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	systems, err := fetchCollection[rfComputerSystem](api, c.root.Systems.ODataID)
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

func mapSystem(s *rfComputerSystem) SystemInfo {
	return SystemInfo{
		ID:           s.ID,
		Name:         s.Name,
		UUID:         s.UUID,
		Serial:       s.SerialNumber,
		Model:        s.Model,
		Manufacturer: s.Manufacturer,
		PowerState:   s.PowerState,
		ODataID:      s.ODataID,
	}
}

func (c *client) computerSystem(systemID string) (*rfComputerSystem, error) {
	api, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	systems, err := fetchCollection[rfComputerSystem](api, c.root.Systems.ODataID)
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
	f := sys.bootFields()
	return BootInfo{
		OverrideEnabled: f.BootSourceOverrideEnabled,
		OverrideTarget:  f.BootSourceOverrideTarget,
	}, nil
}

// setBoot PATCHes the system's Boot object (mirrors gofish's
// (*ComputerSystem).SetBoot, which PATCHes {"Boot": b} to the system's own
// @odata.id). Like gofish, the full Boot object last read from the BMC is
// sent back with only BootSourceOverrideEnabled/Target mutated, rather than
// just those two properties -- some BMCs implement this PATCH as a full
// replace, not a JSON merge-patch, and would otherwise silently reset every
// other Boot property (BootSourceOverrideMode, BootOrder, ...) to firmware
// defaults.
func (c *client) setBoot(sys *rfComputerSystem, enabled, target string) error {
	api, err := c.apiClient()
	if err != nil {
		return err
	}
	var boot map[string]any
	if len(sys.Boot) > 0 {
		_ = json.Unmarshal(sys.Boot, &boot)
	}
	if boot == nil {
		boot = map[string]any{}
	}
	boot["BootSourceOverrideEnabled"] = enabled
	boot["BootSourceOverrideTarget"] = target
	resp, err := api.Patch(sys.ODataID, map[string]any{"Boot": boot})
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
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
	primary := bootTargetCd
	if vendor == VendorDell {
		primary = bootTargetUsbCd
	}
	if cur.OverrideEnabled == bootOverrideOnce &&
		(cur.OverrideTarget == primary || cur.OverrideTarget == bootTargetCd || cur.OverrideTarget == bootTargetUsbCd) {
		return nil
	}
	if err := c.setBoot(sys, bootOverrideOnce, primary); err != nil {
		if primary != bootTargetCd {
			if err2 := c.setBoot(sys, bootOverrideOnce, bootTargetCd); err2 == nil {
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
	managers, err := c.managers()
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
	if (cur.OverrideEnabled == bootOverrideDisabled || cur.OverrideEnabled == "") &&
		(cur.OverrideTarget == bootTargetHdd || cur.OverrideTarget == "") {
		return nil
	}
	if err := c.setBoot(sys, bootOverrideDisabled, bootTargetHdd); err != nil {
		// Retry with Continuous/Hdd if Disabled is rejected by some firmwares.
		if err2 := c.setBoot(sys, bootOverrideContinuous, bootTargetHdd); err2 != nil {
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
		vms, vmErr := fetchCollection[rfVirtualMedia](api, sys.VirtualMedia.ODataID)
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
	managers, err := c.managers()
	if err == nil {
		for _, m := range managers {
			if systemID != "" && m.ID != systemID && m.UUID != systemID {
				// Still allow empty systemID (list all) for diagnostics.
				continue
			}
			vms, vmErr := fetchCollection[rfVirtualMedia](api, m.VirtualMedia.ODataID)
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
		managers, err = c.managers()
		if err == nil {
			for _, m := range managers {
				vms, vmErr := fetchCollection[rfVirtualMedia](api, m.VirtualMedia.ODataID)
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

func mapVM(vm *rfVirtualMedia) VirtualMedia {
	m := VirtualMedia{
		URI:      vm.ODataID,
		Name:     vm.Name,
		ID:       vm.ID,
		Image:    vm.Image,
		Inserted: vm.Inserted,
	}
	for _, t := range vm.MediaTypes {
		m.MediaTypes = append(m.MediaTypes, t)
		if t == "CD" || t == "DVD" {
			m.SupportsCD = true
		}
	}
	if len(vm.MediaTypes) == 0 {
		// assume CD-capable when types omitted (some emulators)
		m.SupportsCD = true
	}
	return m
}

func (c *client) virtualMediaByURI(mediaURI string) (*rfVirtualMedia, error) {
	api, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	// Search systems
	systems, err := fetchCollection[rfComputerSystem](api, c.root.Systems.ODataID)
	if err == nil {
		for _, s := range systems {
			vms, vmErr := fetchCollection[rfVirtualMedia](api, s.VirtualMedia.ODataID)
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
	managers, err := c.managers()
	if err == nil {
		for _, m := range managers {
			vms, vmErr := fetchCollection[rfVirtualMedia](api, m.VirtualMedia.ODataID)
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

// insertMedia POSTs to the VirtualMedia resource's InsertMedia action
// (mirrors gofish's (*VirtualMedia).InsertMedia).
func (c *client) insertMedia(vm *rfVirtualMedia, imageURL string) error {
	api, err := c.apiClient()
	if err != nil {
		return err
	}
	target := vm.Actions.InsertMedia.Target
	if target == "" {
		return fmt.Errorf("redfish: virtual media %q does not support InsertMedia", vm.ODataID)
	}
	payload := map[string]any{
		"Image":          imageURL,
		"Inserted":       true,
		"WriteProtected": true,
	}
	resp, err := api.Post(target, payload)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// ejectMedia POSTs to the VirtualMedia resource's EjectMedia action
// (mirrors gofish's (*VirtualMedia).EjectMedia).
func (c *client) ejectMedia(vm *rfVirtualMedia) error {
	api, err := c.apiClient()
	if err != nil {
		return err
	}
	target := vm.Actions.EjectMedia.Target
	if target == "" {
		return fmt.Errorf("redfish: virtual media %q does not support EjectMedia", vm.ODataID)
	}
	resp, err := api.Post(target, struct{}{})
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
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
		if err := c.ejectMedia(vm); err != nil {
			return fmt.Errorf("redfish: eject before insert: %w", err)
		}
	}
	if err := c.insertMedia(vm, imageURL); err != nil {
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
	if err := c.ejectMedia(vm); err != nil {
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
	// Client-side validation against the system's advertised AllowableValues,
	// mirroring gofish's ComputerSystem.Reset: an empty/unpopulated list means
	// the BMC didn't advertise one, so assume the reset type is fine (gofish
	// does the same).
	if allowed := sys.Actions.Reset.AllowedResetTypes; len(allowed) > 0 {
		valid := false
		for _, a := range allowed {
			if a == resetType {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("redfish: reset type %q is not supported by this service", resetType)
		}
	}
	api, err := c.apiClient()
	if err != nil {
		return err
	}
	resp, err := api.Post(sys.Actions.Reset.Target, map[string]string{"ResetType": resetType})
	if err != nil {
		return fmt.Errorf("redfish: power %s: %w", resetType, err)
	}
	_ = resp.Body.Close()
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
