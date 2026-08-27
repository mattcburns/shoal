package netbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mattcburns/shoal/internal/common/directory"
	"github.com/mattcburns/shoal/internal/common/models"
)

// Client is a minimal NetBox REST client.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
	// Lab defaults for create
	SiteSlug         string
	DeviceRoleSlug   string
	ManufacturerName string
}

// New constructs a Client. baseURL e.g. http://192.168.122.100:8000
func New(baseURL, token string) *Client {
	return &Client{
		BaseURL:          strings.TrimRight(baseURL, "/"),
		Token:            token,
		HTTP:             &http.Client{Timeout: 30 * time.Second},
		SiteSlug:         "shoal-lab",
		DeviceRoleSlug:   "virtual-bmc-node",
		ManufacturerName: "Shoal Virtual",
	}
}

// API is the surface Discover depends on (identity upsert).
type API interface {
	UpsertDevice(ctx context.Context, id models.DeviceIdentity) (string, error)
}

// LifecycleWriter is the Deploy surface for NetBox lifecycle_state only.
// Core never calls this; Discover uses UpsertDevice on ingest.
type LifecycleWriter interface {
	// SetLifecycle updates lifecycle_state for a device identified by NetBox id
	// or serial (serial lookup first, then numeric id).
	SetLifecycle(ctx context.Context, deviceKey string, state models.LifecycleState) error
}

// DeviceResolver maps operator-facing device keys (name, serial, or NetBox
// numeric id) to the NetBox primary key string Shoal uses as device_id.
// Optional on Deploy; when present, Start remaps hostname-style lab keys
// (e.g. shoal-node-1) so NetBox plugin tabs (keyed by device.pk) see jobs.
type DeviceResolver interface {
	ResolveDeviceID(ctx context.Context, key string) (string, error)
}

// UpsertDevice finds a device by serial or creates one; sets lifecycle custom field when possible.
func (c *Client) UpsertDevice(ctx context.Context, id models.DeviceIdentity) (string, error) {
	if c.BaseURL == "" || c.Token == "" {
		return "", fmt.Errorf("netbox: missing url or token")
	}
	if strings.TrimSpace(id.Serial) == "" {
		return "", fmt.Errorf("netbox: serial required")
	}
	existing, err := c.findBySerial(ctx, id.Serial)
	if err != nil {
		return "", err
	}
	if existing != "" {
		if err := c.patchDevice(ctx, existing, id); err != nil {
			return existing, err
		}
		return existing, nil
	}
	return c.createDevice(ctx, id)
}

func (c *Client) findBySerial(ctx context.Context, serial string) (string, error) {
	q := url.Values{"serial": {serial}}
	var out struct {
		Results []struct {
			ID int `json:"id"`
		} `json:"results"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/dcim/devices/?"+q.Encode(), nil, &out); err != nil {
		return "", err
	}
	if len(out.Results) == 0 {
		return "", nil
	}
	return fmt.Sprintf("%d", out.Results[0].ID), nil
}

func (c *Client) findByName(ctx context.Context, name string) (string, error) {
	q := url.Values{"name": {name}}
	var out struct {
		Results []struct {
			ID int `json:"id"`
		} `json:"results"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/dcim/devices/?"+q.Encode(), nil, &out); err != nil {
		return "", err
	}
	if len(out.Results) == 0 {
		return "", nil
	}
	return fmt.Sprintf("%d", out.Results[0].ID), nil
}

// ResolveDeviceID implements DeviceResolver and directory.Store.
// Order: serial match, name match, then (only if key already looks like a
// NetBox numeric id) return it unchanged. Anything else that matches
// nothing returns directory.ErrNotFound.
func (c *Client) ResolveDeviceID(ctx context.Context, key string) (string, error) {
	if c.BaseURL == "" || c.Token == "" {
		return "", fmt.Errorf("netbox: missing url or token")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("netbox: device key required")
	}
	if id, err := c.findBySerial(ctx, key); err != nil {
		return "", err
	} else if id != "" {
		return id, nil
	}
	if id, err := c.findByName(ctx, key); err != nil {
		return "", err
	} else if id != "" {
		return id, nil
	}
	// A key that already looks like a NetBox numeric id is passed through
	// unchanged (it may be a valid pk this method was never asked to verify
	// against serial/name) -- this is the long-standing behavior orchestrator
	// callers rely on. Anything else (a typo'd or unknown serial/name/key)
	// genuinely doesn't resolve to a device, so report that rather than
	// silently handing back a value that will only fail later, differently,
	// at whatever endpoint the caller passes it to next.
	if looksLikeNetBoxID(key) {
		return key, nil
	}
	return "", directory.ErrNotFound
}

func looksLikeNetBoxID(key string) bool {
	if key == "" {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < '0' || key[i] > '9' {
			return false
		}
	}
	return true
}

// rawDeviceResponse is NetBox's DCIM device JSON shape, as returned by both
// GET /api/dcim/devices/{id}/ and each entry of GET /api/dcim/devices/'s
// paginated results list. toIdentity() is the single place that maps it to
// models.DeviceIdentity, shared by GetDevice and ListDevices.
type rawDeviceResponse struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Serial string `json:"serial"`
	CF     struct {
		LifecycleState string `json:"lifecycle_state"`
		CredentialRef  string `json:"credential_ref"`
		BMCIP          string `json:"bmc_ip"`
	} `json:"custom_fields"`
	DeviceType struct {
		Model        string `json:"model"`
		Manufacturer struct {
			Name string `json:"name"`
		} `json:"manufacturer"`
	} `json:"device_type"`
}

func (r rawDeviceResponse) toIdentity() models.DeviceIdentity {
	return models.DeviceIdentity{
		ID:             fmt.Sprintf("%d", r.ID),
		Name:           r.Name,
		Serial:         r.Serial,
		Vendor:         r.DeviceType.Manufacturer.Name,
		Model:          r.DeviceType.Model,
		LifecycleState: models.LifecycleState(r.CF.LifecycleState),
		CredentialRef:  r.CF.CredentialRef,
		BMCIP:          r.CF.BMCIP,
	}
}

// GetDevice loads identity by serial, name, or NetBox id. Password is never
// included. Implements directory.Store; returns directory.ErrNotFound when
// NetBox responds 404 (also covers "no such serial/name and the fallback
// numeric lookup misses").
func (c *Client) GetDevice(ctx context.Context, key string) (models.DeviceIdentity, error) {
	if c.BaseURL == "" || c.Token == "" {
		return models.DeviceIdentity{}, fmt.Errorf("netbox: missing url or token")
	}
	key = strings.TrimSpace(key)
	id := key
	if !looksLikeNetBoxID(key) {
		resolved, err := c.ResolveDeviceID(ctx, key)
		if err != nil {
			return models.DeviceIdentity{}, err
		}
		id = resolved
	}
	var raw rawDeviceResponse
	if err := c.doJSON(ctx, http.MethodGet, "/api/dcim/devices/"+id+"/", nil, &raw); err != nil {
		if isNotFound(err) {
			return models.DeviceIdentity{}, directory.ErrNotFound
		}
		return models.DeviceIdentity{}, err
	}
	out := raw.toIdentity()
	if out.ID == "0" && id != "" {
		out.ID = id
	}
	return out, nil
}

// ListDevices implements directory.Store. It follows NetBox's standard
// {"count","next","results"} pagination envelope until "next" is null.
func (c *Client) ListDevices(ctx context.Context) ([]models.DeviceIdentity, error) {
	if c.BaseURL == "" || c.Token == "" {
		return nil, fmt.Errorf("netbox: missing url or token")
	}
	var out []models.DeviceIdentity
	path := "/api/dcim/devices/"
	for path != "" {
		var page struct {
			Next    *string             `json:"next"`
			Results []rawDeviceResponse `json:"results"`
		}
		if err := c.doJSON(ctx, http.MethodGet, path, nil, &page); err != nil {
			return nil, err
		}
		for _, r := range page.Results {
			out = append(out, r.toIdentity())
		}
		if page.Next == nil || strings.TrimSpace(*page.Next) == "" {
			break
		}
		next, err := url.Parse(*page.Next)
		if err != nil {
			return nil, fmt.Errorf("netbox: bad next page url: %w", err)
		}
		path = next.RequestURI()
	}
	return out, nil
}

// DeleteDevice implements directory.Store. Like GetDevice and SetLifecycle,
// the key may be a NetBox numeric id, a serial, or a name (resolved via
// ResolveDeviceID) so callers don't need to know which form they hold; a
// bare numeric id skips resolution and deletes directly. Treats a 404 from
// NetBox as directory.ErrNotFound; any 2xx (200 or 204) is success.
func (c *Client) DeleteDevice(ctx context.Context, key string) error {
	if c.BaseURL == "" || c.Token == "" {
		return fmt.Errorf("netbox: missing url or token")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("netbox: device id required")
	}
	id := key
	if !looksLikeNetBoxID(key) {
		resolved, err := c.ResolveDeviceID(ctx, key)
		if err != nil {
			return err
		}
		id = resolved
	}
	if err := c.doJSON(ctx, http.MethodDelete, "/api/dcim/devices/"+id+"/", nil, nil); err != nil {
		if isNotFound(err) {
			return directory.ErrNotFound
		}
		return err
	}
	return nil
}

func (c *Client) createDevice(ctx context.Context, id models.DeviceIdentity) (string, error) {
	siteID, err := c.lookupID(ctx, "/api/dcim/sites/", "slug", c.SiteSlug)
	if err != nil {
		return "", fmt.Errorf("netbox: site: %w", err)
	}
	roleID, _, typeID, err := c.ensureClassification(ctx, id)
	if err != nil {
		return "", err
	}
	name := id.Name
	if name == "" {
		name = id.Serial
	}
	body := map[string]any{
		"name":        name,
		"serial":      id.Serial,
		"site":        siteID,
		"role":        roleID,
		"device_type": typeID,
		"status":      "inventory",
		"custom_fields": map[string]any{
			"lifecycle_state": string(id.LifecycleState),
			"credential_ref":  id.CredentialRef,
			"bmc_ip":          id.BMCIP,
		},
	}
	var created struct {
		ID int `json:"id"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/api/dcim/devices/", body, &created); err != nil {
		// Retry without custom fields if CF not defined in this NetBox.
		if strings.Contains(err.Error(), "custom_fields") || strings.Contains(err.Error(), "400") {
			delete(body, "custom_fields")
			body["comments"] = fmt.Sprintf("lifecycle_state=%s credential_ref=%s bmc_ip=%s",
				id.LifecycleState, id.CredentialRef, id.BMCIP)
			if err2 := c.doJSON(ctx, http.MethodPost, "/api/dcim/devices/", body, &created); err2 != nil {
				return "", err2
			}
		} else {
			return "", err
		}
	}
	return fmt.Sprintf("%d", created.ID), nil
}

func (c *Client) patchDevice(ctx context.Context, netboxID string, id models.DeviceIdentity) error {
	body := map[string]any{
		"serial": id.Serial,
		"custom_fields": map[string]any{
			"lifecycle_state": string(id.LifecycleState),
			"credential_ref":  id.CredentialRef,
			"bmc_ip":          id.BMCIP,
		},
	}
	if id.Name != "" {
		body["name"] = id.Name
	}
	if roleID, _, typeID, err := c.ensureClassification(ctx, id); err == nil {
		body["role"] = roleID
		body["device_type"] = typeID
	}
	err := c.doJSON(ctx, http.MethodPatch, "/api/dcim/devices/"+netboxID+"/", body, nil)
	if err != nil {
		// fallback comments only
		fb := map[string]any{
			"comments": fmt.Sprintf("lifecycle_state=%s credential_ref=%s bmc_ip=%s",
				id.LifecycleState, id.CredentialRef, id.BMCIP),
		}
		return c.doJSON(ctx, http.MethodPatch, "/api/dcim/devices/"+netboxID+"/", fb, nil)
	}
	return nil
}

// SetLifecycle implements LifecycleWriter.
func (c *Client) SetLifecycle(ctx context.Context, deviceKey string, state models.LifecycleState) error {
	if c.BaseURL == "" || c.Token == "" {
		return fmt.Errorf("netbox: missing url or token")
	}
	deviceKey = strings.TrimSpace(deviceKey)
	if deviceKey == "" {
		return fmt.Errorf("netbox: device key required")
	}
	if state == "" {
		return fmt.Errorf("netbox: lifecycle state required")
	}
	netboxID, err := c.findBySerial(ctx, deviceKey)
	if err != nil {
		return err
	}
	if netboxID == "" {
		// Treat key as NetBox numeric id when serial lookup misses.
		netboxID = deviceKey
	}
	body := map[string]any{
		"custom_fields": map[string]any{
			"lifecycle_state": string(state),
		},
	}
	err = c.doJSON(ctx, http.MethodPatch, "/api/dcim/devices/"+netboxID+"/", body, nil)
	if err != nil {
		fb := map[string]any{
			"comments": fmt.Sprintf("lifecycle_state=%s", state),
		}
		return c.doJSON(ctx, http.MethodPatch, "/api/dcim/devices/"+netboxID+"/", fb, nil)
	}
	return nil
}

// SetCredentialRef writes credential_ref (and optional bmc_ip) custom fields only.
// It never creates a device and never changes role, type, or lifecycle_state.
func (c *Client) SetCredentialRef(ctx context.Context, deviceKey, ref, bmcIP string) error {
	if c.BaseURL == "" || c.Token == "" {
		return fmt.Errorf("netbox: missing url or token")
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return fmt.Errorf("netbox: credential_ref required")
	}
	id, err := c.GetDevice(ctx, deviceKey)
	if err != nil {
		return err
	}
	netboxID := strings.TrimSpace(id.ID)
	if netboxID == "" || netboxID == "0" {
		return fmt.Errorf("netbox: device %q not found", deviceKey)
	}
	cf := map[string]any{"credential_ref": ref}
	if ip := strings.TrimSpace(bmcIP); ip != "" {
		cf["bmc_ip"] = ip
	}
	body := map[string]any{"custom_fields": cf}
	return c.doJSON(ctx, http.MethodPatch, "/api/dcim/devices/"+netboxID+"/", body, nil)
}

func (c *Client) lookupID(ctx context.Context, path, filterKey, filterVal string) (int, error) {
	q := url.Values{filterKey: {filterVal}}
	var out struct {
		Results []struct {
			ID int `json:"id"`
		} `json:"results"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path+"?"+q.Encode(), nil, &out); err != nil {
		return 0, err
	}
	if len(out.Results) == 0 {
		return 0, fmt.Errorf("not found %s=%s", filterKey, filterVal)
	}
	return out.Results[0].ID, nil
}

const physicalServerRoleSlug = "server"

func labVirtualIdentity(id models.DeviceIdentity) bool {
	v := strings.TrimSpace(id.Vendor)
	m := strings.TrimSpace(id.Model)
	if v == "" && m == "" {
		return true
	}
	if strings.EqualFold(v, "Shoal Virtual") {
		return true
	}
	ml := strings.ToLower(m)
	return strings.Contains(ml, "sushy") || strings.HasPrefix(ml, "shoal-")
}

func (c *Client) ensureClassification(ctx context.Context, id models.DeviceIdentity) (roleID, mfgID, typeID int, err error) {
	roleSlug := c.DeviceRoleSlug
	roleName := "Virtual BMC Node"
	mfgName := c.ManufacturerName
	model := "shoal-node"
	if id.Name != "" {
		model = "shoal-" + id.Name
		if len(model) > 40 {
			model = model[:40]
		}
	}
	if !labVirtualIdentity(id) {
		roleSlug = physicalServerRoleSlug
		roleName = "Server"
		if v := strings.TrimSpace(id.Vendor); v != "" {
			mfgName = v
		}
		if m := strings.TrimSpace(id.Model); m != "" {
			model = m
			if len(model) > 64 {
				model = model[:64]
			}
		}
	}
	roleID, err = c.ensureDeviceRole(ctx, roleSlug, roleName)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("netbox: role: %w", err)
	}
	mfgID, err = c.ensureManufacturer(ctx, mfgName)
	if err != nil {
		return 0, 0, 0, err
	}
	typeID, err = c.ensureDeviceType(ctx, mfgID, model)
	if err != nil {
		return 0, 0, 0, err
	}
	return roleID, mfgID, typeID, nil
}

func (c *Client) ensureDeviceRole(ctx context.Context, slug, name string) (int, error) {
	id, err := c.lookupID(ctx, "/api/dcim/device-roles/", "slug", slug)
	if err == nil {
		return id, nil
	}
	var created struct {
		ID int `json:"id"`
	}
	body := map[string]any{
		"name":    name,
		"slug":    slug,
		"color":   "2196f3",
		"vm_role": false,
	}
	if err := c.doJSON(ctx, http.MethodPost, "/api/dcim/device-roles/", body, &created); err != nil {
		return c.lookupID(ctx, "/api/dcim/device-roles/", "slug", slug)
	}
	return created.ID, nil
}

func (c *Client) ensureManufacturer(ctx context.Context, name string) (int, error) {
	if strings.TrimSpace(name) == "" {
		name = c.ManufacturerName
	}
	id, err := c.lookupID(ctx, "/api/dcim/manufacturers/", "name", name)
	if err == nil {
		return id, nil
	}
	var created struct {
		ID int `json:"id"`
	}
	body := map[string]any{"name": name, "slug": netboxSlug(name)}
	if err := c.doJSON(ctx, http.MethodPost, "/api/dcim/manufacturers/", body, &created); err != nil {
		return c.lookupID(ctx, "/api/dcim/manufacturers/", "name", name)
	}
	return created.ID, nil
}

func (c *Client) ensureDeviceType(ctx context.Context, mfgID int, model string) (int, error) {
	if strings.TrimSpace(model) == "" {
		model = "shoal-node"
	}
	// NetBox's actual uniqueness constraint on device types is the
	// (manufacturer, model) pair, not model alone -- filtering by model only
	// would let this lookup return a different manufacturer's device type
	// that happens to share the same model string, silently reusing it
	// (and its manufacturer) instead of creating/finding the right one for
	// mfgID. This matters on every UpsertDevice update: a device's vendor
	// changing while its derived model string stays the same (e.g. an empty
	// Model field, so model defaults to "shoal-"+name) must not pin the
	// device to its previous manufacturer.
	q := url.Values{"model": {model}, "manufacturer_id": {strconv.Itoa(mfgID)}}
	var out struct {
		Results []struct {
			ID int `json:"id"`
		} `json:"results"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/dcim/device-types/?"+q.Encode(), nil, &out); err == nil && len(out.Results) > 0 {
		return out.Results[0].ID, nil
	}
	body := map[string]any{
		"manufacturer": mfgID,
		"model":        model,
		"slug":         netboxSlug(model),
	}
	var created struct {
		ID int `json:"id"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/api/dcim/device-types/", body, &created); err != nil {
		if err2 := c.doJSON(ctx, http.MethodGet, "/api/dcim/device-types/?"+q.Encode(), nil, &out); err2 == nil && len(out.Results) > 0 {
			return out.Results[0].ID, nil
		}
		return 0, err
	}
	return created.ID, nil
}

func netboxSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	hyphen := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			hyphen = false
			continue
		}
		if !hyphen {
			b.WriteByte('-')
			hyphen = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unknown"
	}
	if len(out) > 50 {
		out = out[:50]
	}
	return out
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Token "+c.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &httpStatusError{
			status: resp.StatusCode,
			err:    fmt.Errorf("netbox: %s %s: status %d: %s", method, path, resp.StatusCode, truncate(string(raw), 300)),
		}
	}
	if out == nil || len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("netbox: decode: %w", err)
	}
	return nil
}

// httpStatusError preserves the HTTP status code of a non-2xx NetBox
// response so callers (GetDevice, DeleteDevice) can distinguish 404 from
// other failures and map it to directory.ErrNotFound. Error()/Unwrap()
// delegate to the formatted error so existing string-matching callers
// (e.g. createDevice's custom_fields retry) are unaffected.
type httpStatusError struct {
	status int
	err    error
}

func (e *httpStatusError) Error() string { return e.err.Error() }
func (e *httpStatusError) Unwrap() error { return e.err }

func isNotFound(err error) bool {
	var hse *httpStatusError
	return errors.As(err, &hse) && hse.status == http.StatusNotFound
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Memory is an in-memory NetBox fake for tests.
type Memory struct {
	BySerial map[string]models.DeviceIdentity
	ByID     map[string]models.DeviceIdentity
	Next     int
}

// NewMemory constructs a Memory API.
func NewMemory() *Memory {
	return &Memory{
		BySerial: map[string]models.DeviceIdentity{},
		ByID:     map[string]models.DeviceIdentity{},
		Next:     1,
	}
}

// UpsertDevice implements API.
func (m *Memory) UpsertDevice(_ context.Context, id models.DeviceIdentity) (string, error) {
	if id.Serial == "" {
		return "", fmt.Errorf("netbox/memory: serial required")
	}
	if existing, ok := m.BySerial[id.Serial]; ok && existing.ID != "" {
		id.ID = existing.ID
		m.BySerial[id.Serial] = id
		m.ByID[id.ID] = id
		return id.ID, nil
	}
	id.ID = fmt.Sprintf("%d", m.Next)
	m.Next++
	m.BySerial[id.Serial] = id
	m.ByID[id.ID] = id
	return id.ID, nil
}

// SetLifecycle implements LifecycleWriter.
func (m *Memory) SetLifecycle(_ context.Context, deviceKey string, state models.LifecycleState) error {
	if deviceKey == "" || state == "" {
		return fmt.Errorf("netbox/memory: device key and state required")
	}
	if id, ok := m.BySerial[deviceKey]; ok {
		id.LifecycleState = state
		m.BySerial[deviceKey] = id
		if id.ID != "" {
			m.ByID[id.ID] = id
		}
		return nil
	}
	if id, ok := m.ByID[deviceKey]; ok {
		id.LifecycleState = state
		m.ByID[deviceKey] = id
		if id.Serial != "" {
			m.BySerial[id.Serial] = id
		}
		return nil
	}
	return fmt.Errorf("netbox/memory: device %q not found", deviceKey)
}

// GetDevice implements lookup for credentials/power.
func (m *Memory) GetDevice(_ context.Context, key string) (models.DeviceIdentity, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return models.DeviceIdentity{}, fmt.Errorf("netbox/memory: device key required")
	}
	if id, ok := m.ByID[key]; ok {
		return id, nil
	}
	if id, ok := m.BySerial[key]; ok {
		return id, nil
	}
	for _, id := range m.ByID {
		if id.Name == key {
			return id, nil
		}
	}
	return models.DeviceIdentity{}, fmt.Errorf("netbox/memory: device %q not found", key)
}

// SetCredentialRef implements credential_ref (+ optional bmc_ip) update for tests.
func (m *Memory) SetCredentialRef(ctx context.Context, deviceKey, ref, bmcIP string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return fmt.Errorf("netbox/memory: credential_ref required")
	}
	id, err := m.GetDevice(ctx, deviceKey)
	if err != nil {
		return err
	}
	id.CredentialRef = ref
	if ip := strings.TrimSpace(bmcIP); ip != "" {
		id.BMCIP = ip
	}
	if id.Serial != "" {
		m.BySerial[id.Serial] = id
	}
	if id.ID != "" {
		m.ByID[id.ID] = id
	}
	return nil
}

// ResolveDeviceID implements DeviceResolver for tests/lab fakes.
func (m *Memory) ResolveDeviceID(_ context.Context, key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("netbox/memory: device key required")
	}
	if id, ok := m.BySerial[key]; ok && id.ID != "" {
		return id.ID, nil
	}
	// Name match: Memory stores Name on DeviceIdentity when set.
	for _, id := range m.ByID {
		if id.Name == key && id.ID != "" {
			return id.ID, nil
		}
		// Common lab case: name == serial.
		if id.Serial == key && id.ID != "" {
			return id.ID, nil
		}
	}
	if id, ok := m.ByID[key]; ok && id.ID != "" {
		return id.ID, nil
	}
	return key, nil
}

var _ API = (*Client)(nil)
var _ API = (*Memory)(nil)
var _ LifecycleWriter = (*Client)(nil)
var _ LifecycleWriter = (*Memory)(nil)
var _ DeviceResolver = (*Client)(nil)
var _ DeviceResolver = (*Memory)(nil)

// Client satisfies the device-directory Store abstraction (see
// internal/common/directory) via ListDevices/GetDevice/UpsertDevice/
// SetLifecycle/ResolveDeviceID/DeleteDevice added above.
var _ directory.Store = (*Client)(nil)
