package netbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattcburns/shoal/internal/common/directory"
	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/netbox"
)

func TestMemoryUpsert(t *testing.T) {
	m := netbox.NewMemory()
	id, err := m.UpsertDevice(context.Background(), models.DeviceIdentity{
		Serial: "S1", BMCIP: "1.1.1.1", LifecycleState: models.StateDiscovered,
	})
	if err != nil || id == "" {
		t.Fatalf("%v %q", err, id)
	}
	id2, err := m.UpsertDevice(context.Background(), models.DeviceIdentity{
		Serial: "S1", BMCIP: "1.1.1.2", LifecycleState: models.StateReady,
	})
	if err != nil || id2 != id {
		t.Fatalf("update %v %q", err, id2)
	}
}

func TestMemorySetLifecycle(t *testing.T) {
	m := netbox.NewMemory()
	_, err := m.UpsertDevice(context.Background(), models.DeviceIdentity{
		Serial: "NODE-1", LifecycleState: models.StateDiscovered,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetLifecycle(context.Background(), "NODE-1", models.StateProvisioning); err != nil {
		t.Fatal(err)
	}
	if m.BySerial["NODE-1"].LifecycleState != models.StateProvisioning {
		t.Fatalf("%+v", m.BySerial["NODE-1"])
	}
	if err := m.SetLifecycle(context.Background(), "missing", models.StateFailed); err == nil {
		t.Fatal("expected not found")
	}
}

func TestMemoryGetDeviceAndSetCredentialRef(t *testing.T) {
	m := netbox.NewMemory()
	id, err := m.UpsertDevice(context.Background(), models.DeviceIdentity{
		Name: "C784MH3", Serial: "C784MH3", BMCIP: "172.16.21.202",
		Vendor: "Dell Inc.", Model: "PowerEdge R750",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.GetDevice(context.Background(), id)
	if err != nil || got.Serial != "C784MH3" || got.Vendor != "Dell Inc." {
		t.Fatalf("%+v %v", got, err)
	}
	if err := m.SetCredentialRef(context.Background(), id, "bmc-C784MH3", ""); err != nil {
		t.Fatal(err)
	}
	got, err = m.GetDevice(context.Background(), "C784MH3")
	if err != nil {
		t.Fatal(err)
	}
	if got.CredentialRef != "bmc-C784MH3" || got.BMCIP != "172.16.21.202" {
		t.Fatalf("%+v", got)
	}
	if got.Vendor != "Dell Inc." || got.Model != "PowerEdge R750" {
		t.Fatalf("classification wiped: %+v", got)
	}
	if err := m.SetCredentialRef(context.Background(), "missing", "bmc-x", ""); err == nil {
		t.Fatal("expected not found")
	}
}

func TestMemoryResolveDeviceID(t *testing.T) {
	m := netbox.NewMemory()
	id, err := m.UpsertDevice(context.Background(), models.DeviceIdentity{
		Name: "shoal-node-1", Serial: "shoal-node-1", LifecycleState: models.StateDiscovered,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.ResolveDeviceID(context.Background(), "shoal-node-1")
	if err != nil || got != id {
		t.Fatalf("serial resolve: %v %q want %q", err, got, id)
	}
	got, err = m.ResolveDeviceID(context.Background(), id)
	if err != nil || got != id {
		t.Fatalf("id passthrough: %v %q", err, got)
	}
	got, err = m.ResolveDeviceID(context.Background(), "unknown-lab-key")
	if err != nil || got != "unknown-lab-key" {
		t.Fatalf("unknown keep: %v %q", err, got)
	}
}

func TestClientResolveDeviceID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/dcim/devices/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		q := r.URL.Query()
		if q.Get("serial") == "shoal-node-1" || q.Get("name") == "shoal-node-1" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []any{map[string]any{"id": 3}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := netbox.New(srv.URL, "tok")
	got, err := c.ResolveDeviceID(context.Background(), "shoal-node-1")
	if err != nil || got != "3" {
		t.Fatalf("got %q err %v", got, err)
	}
	// A numeric key that matches nothing by serial/name is assumed to
	// already be a valid NetBox pk and passed through unchanged.
	got, err = c.ResolveDeviceID(context.Background(), "42")
	if err != nil || got != "42" {
		t.Fatalf("numeric passthrough %q %v", got, err)
	}
	// A non-numeric key that matches nothing by serial/name genuinely
	// doesn't resolve to a device -- report that instead of silently
	// handing back a value that can only fail later, differently.
	got, err = c.ResolveDeviceID(context.Background(), "no-such")
	if !errors.Is(err, directory.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unresolvable non-numeric key, got %q %v", got, err)
	}
}

func TestClientGetDeviceByNumericIDSkipsList(t *testing.T) {
	var listed bool
	mux := http.NewServeMux()
	mux.HandleFunc("/api/dcim/devices/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.RawQuery != "" {
			listed = true
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 6, "name": "C784MH3", "serial": "C784MH3",
			"custom_fields": map[string]any{"credential_ref": "bmc-C784MH3", "bmc_ip": "172.16.21.202"},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := netbox.New(srv.URL, "tok")
	got, err := c.GetDevice(context.Background(), "6")
	if err != nil {
		t.Fatal(err)
	}
	if listed {
		t.Fatal("numeric NetBox id must not search by serial/name")
	}
	if got.ID != "6" || got.CredentialRef != "bmc-C784MH3" {
		t.Fatalf("%+v", got)
	}
}

func TestClientGetDevice(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/dcim/devices/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.RawQuery != "" {
			if r.URL.Query().Get("serial") == "C784MH3" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"results": []any{map[string]any{"id": 6}},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 6, "name": "C784MH3", "serial": "C784MH3",
			"custom_fields": map[string]any{
				"lifecycle_state": "discovered",
				"credential_ref":  "bmc-C784MH3",
				"bmc_ip":          "172.16.21.202",
			},
			"device_type": map[string]any{
				"model":        "PowerEdge R750",
				"manufacturer": map[string]any{"name": "Dell Inc."},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := netbox.New(srv.URL, "tok")
	got, err := c.GetDevice(context.Background(), "C784MH3")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "6" || got.Serial != "C784MH3" || got.Vendor != "Dell Inc." || got.Model != "PowerEdge R750" {
		t.Fatalf("%+v", got)
	}
	if got.CredentialRef != "bmc-C784MH3" || got.BMCIP != "172.16.21.202" {
		t.Fatalf("%+v", got)
	}
}

func TestClientSetCredentialRef(t *testing.T) {
	var patched map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/api/dcim/devices/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if r.URL.RawQuery != "" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"results": []any{map[string]any{"id": 6}},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 6, "name": "C784MH3", "serial": "C784MH3",
				"custom_fields": map[string]any{"bmc_ip": "172.16.21.202"},
			})
		case http.MethodPatch:
			_ = json.NewDecoder(r.Body).Decode(&patched)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := netbox.New(srv.URL, "tok")
	if err := c.SetCredentialRef(context.Background(), "6", "bmc-C784MH3", ""); err != nil {
		t.Fatal(err)
	}
	cf, _ := patched["custom_fields"].(map[string]any)
	if cf["credential_ref"] != "bmc-C784MH3" {
		t.Fatalf("patch %+v", patched)
	}
	if _, ok := patched["role"]; ok {
		t.Fatalf("must not patch role: %+v", patched)
	}
	if _, ok := patched["device_type"]; ok {
		t.Fatalf("must not patch device_type: %+v", patched)
	}
	if _, ok := cf["bmc_ip"]; ok {
		t.Fatalf("empty bmc_ip must not overwrite: %+v", patched)
	}
}

func TestClientSetLifecycle(t *testing.T) {
	var patched bool
	mux := http.NewServeMux()
	mux.HandleFunc("/api/dcim/devices/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []any{map[string]any{"id": 7}},
			})
			return
		}
		if r.Method == http.MethodPatch {
			patched = true
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := netbox.New(srv.URL, "tok")
	if err := c.SetLifecycle(context.Background(), "SERIAL-X", models.StateProvisioned); err != nil {
		t.Fatal(err)
	}
	if !patched {
		t.Fatal("expected PATCH")
	}
}

func TestClientFindCreate(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/dcim/devices/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
		case http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 42})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/dcim/sites/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"id": 1}}})
	})
	mux.HandleFunc("/api/dcim/device-roles/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"id": 2}}})
	})
	mux.HandleFunc("/api/dcim/manufacturers/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"id": 3}}})
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 3})
	})
	mux.HandleFunc("/api/dcim/device-types/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 4})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := netbox.New(srv.URL, "token")
	id, err := c.UpsertDevice(context.Background(), models.DeviceIdentity{
		Serial: "SN", Name: "M", BMCIP: "10.0.0.1", LifecycleState: models.StateDiscovered,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != "42" {
		t.Fatalf("id %q", id)
	}
}

func TestClientListDevicesPaginates(t *testing.T) {
	var page1URL string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/dcim/devices/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.RawQuery == "page=2" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"count": 2,
				"next":  nil,
				"results": []any{
					map[string]any{"id": 2, "name": "n2", "serial": "S2"},
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"count": 2,
			"next":  page1URL,
			"results": []any{
				map[string]any{"id": 1, "name": "n1", "serial": "S1"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	page1URL = srv.URL + "/api/dcim/devices/?page=2"

	c := netbox.New(srv.URL, "tok")
	got, err := c.ListDevices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 devices across pages, got %d: %+v", len(got), got)
	}
	if got[0].Serial != "S1" || got[1].Serial != "S2" {
		t.Fatalf("unexpected devices: %+v", got)
	}
}

func TestClientGetDeviceNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/dcim/devices/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := netbox.New(srv.URL, "tok")
	_, err := c.GetDevice(context.Background(), "999")
	if !errors.Is(err, directory.ErrNotFound) {
		t.Fatalf("expected directory.ErrNotFound, got %v", err)
	}
}

func TestClientDeleteDevice(t *testing.T) {
	var deleted bool
	mux := http.NewServeMux()
	mux.HandleFunc("/api/dcim/devices/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			if r.URL.Path == "/api/dcim/devices/5/" {
				deleted = true
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := netbox.New(srv.URL, "tok")
	if err := c.DeleteDevice(context.Background(), "5"); err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("expected DELETE to reach server")
	}
	if err := c.DeleteDevice(context.Background(), "404"); !errors.Is(err, directory.ErrNotFound) {
		t.Fatalf("expected directory.ErrNotFound, got %v", err)
	}
}

// TestClientDeleteDeviceResolvesSerial ensures DeleteDevice resolves a
// non-numeric key (serial/name) to the NetBox id first, same as
// GetDevice/SetLifecycle, instead of sending it straight to the id-keyed
// DELETE route (which would 404 and be misreported as ErrNotFound even
// though the device exists).
func TestClientDeleteDeviceResolvesSerial(t *testing.T) {
	var deletedPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/dcim/devices/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if r.URL.Query().Get("serial") == "SN-DEL" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"results": []any{map[string]any{"id": 8}},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
		case http.MethodDelete:
			deletedPath = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := netbox.New(srv.URL, "tok")
	if err := c.DeleteDevice(context.Background(), "SN-DEL"); err != nil {
		t.Fatal(err)
	}
	if deletedPath != "/api/dcim/devices/8/" {
		t.Fatalf("expected DELETE to resolved id 8, got path %q", deletedPath)
	}
}

func TestClientCreatePhysicalUsesServerRole(t *testing.T) {
	var deviceBody map[string]any
	var typeBody map[string]any
	var mfgBody map[string]any
	var roleBody map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/api/dcim/devices/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
		case http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(&deviceBody)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 9})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/dcim/sites/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"id": 1}}})
	})
	mux.HandleFunc("/api/dcim/device-roles/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&roleBody)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 11})
	})
	mux.HandleFunc("/api/dcim/manufacturers/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&mfgBody)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 12})
	})
	mux.HandleFunc("/api/dcim/device-types/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&typeBody)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 13})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := netbox.New(srv.URL, "token")
	id, err := c.UpsertDevice(context.Background(), models.DeviceIdentity{
		Serial: "C784MH3", Vendor: "Dell Inc.", Model: "PowerEdge R750",
		BMCIP: "172.16.21.202", LifecycleState: models.StateDiscovered,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != "9" {
		t.Fatalf("id %q", id)
	}
	if roleBody["slug"] != "server" || roleBody["name"] != "Server" {
		t.Fatalf("role %+v", roleBody)
	}
	if mfgBody["name"] != "Dell Inc." || mfgBody["slug"] != "dell-inc" {
		t.Fatalf("mfg %+v", mfgBody)
	}
	if typeBody["model"] != "PowerEdge R750" || typeBody["slug"] != "poweredge-r750" {
		t.Fatalf("type %+v", typeBody)
	}
	if deviceBody["role"] != float64(11) || deviceBody["device_type"] != float64(13) {
		t.Fatalf("device %+v", deviceBody)
	}
}
