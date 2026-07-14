// Package netbox implements NetBox REST for identity + lifecycle_state only.
package netbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

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

func (c *Client) createDevice(ctx context.Context, id models.DeviceIdentity) (string, error) {
	siteID, err := c.lookupID(ctx, "/api/dcim/sites/", "slug", c.SiteSlug)
	if err != nil {
		return "", fmt.Errorf("netbox: site: %w", err)
	}
	roleID, err := c.lookupID(ctx, "/api/dcim/device-roles/", "slug", c.DeviceRoleSlug)
	if err != nil {
		return "", fmt.Errorf("netbox: role: %w", err)
	}
	mfgID, err := c.ensureManufacturer(ctx)
	if err != nil {
		return "", err
	}
	typeID, err := c.ensureDeviceType(ctx, mfgID, id)
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

func (c *Client) ensureManufacturer(ctx context.Context) (int, error) {
	id, err := c.lookupID(ctx, "/api/dcim/manufacturers/", "name", c.ManufacturerName)
	if err == nil {
		return id, nil
	}
	var created struct {
		ID int `json:"id"`
	}
	slug := strings.ToLower(strings.ReplaceAll(c.ManufacturerName, " ", "-"))
	body := map[string]any{"name": c.ManufacturerName, "slug": slug}
	if err := c.doJSON(ctx, http.MethodPost, "/api/dcim/manufacturers/", body, &created); err != nil {
		// race: re-get
		return c.lookupID(ctx, "/api/dcim/manufacturers/", "name", c.ManufacturerName)
	}
	return created.ID, nil
}

func (c *Client) ensureDeviceType(ctx context.Context, mfgID int, id models.DeviceIdentity) (int, error) {
	// DeviceIdentity is identity-only; use a stable lab device-type model name.
	model := "shoal-node"
	if id.Name != "" {
		model = "shoal-" + id.Name
		if len(model) > 40 {
			model = model[:40]
		}
	}
	// search by model
	q := url.Values{"model": {model}}
	var out struct {
		Results []struct {
			ID int `json:"id"`
		} `json:"results"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/dcim/device-types/?"+q.Encode(), nil, &out); err == nil && len(out.Results) > 0 {
		return out.Results[0].ID, nil
	}
	slug := strings.ToLower(strings.ReplaceAll(model, " ", "-"))
	body := map[string]any{
		"manufacturer": mfgID,
		"model":        model,
		"slug":         slug,
	}
	var created struct {
		ID int `json:"id"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/api/dcim/device-types/", body, &created); err != nil {
		// try get again
		if err2 := c.doJSON(ctx, http.MethodGet, "/api/dcim/device-types/?"+q.Encode(), nil, &out); err2 == nil && len(out.Results) > 0 {
			return out.Results[0].ID, nil
		}
		return 0, err
	}
	return created.ID, nil
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
		return fmt.Errorf("netbox: %s %s: status %d: %s", method, path, resp.StatusCode, truncate(string(raw), 300))
	}
	if out == nil || len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("netbox: decode: %w", err)
	}
	return nil
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

var _ API = (*Client)(nil)
var _ API = (*Memory)(nil)
var _ LifecycleWriter = (*Client)(nil)
var _ LifecycleWriter = (*Memory)(nil)
