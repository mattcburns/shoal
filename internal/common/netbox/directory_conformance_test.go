package netbox_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/mattcburns/shoal/internal/common/directory"
	"github.com/mattcburns/shoal/internal/common/netbox"
)

// fakeDevice is the in-memory record backing the fake NetBox server below.
type fakeDevice struct {
	ID     int
	Name   string
	Serial string
	CF     map[string]any
}

func fakeDeviceJSON(d *fakeDevice) map[string]any {
	return map[string]any{
		"id":            d.ID,
		"name":          d.Name,
		"serial":        d.Serial,
		"custom_fields": d.CF,
		"device_type": map[string]any{
			"model":        "fake-model",
			"manufacturer": map[string]any{"name": "fake-vendor"},
		},
	}
}

// newFakeNetBoxServer builds a minimal in-memory httptest fake of the NetBox
// DCIM device endpoints (list/get/create/update/delete, plus the
// classification lookups UpsertDevice's create path needs) sufficient to
// drive directory.RunConformance end to end against a real *netbox.Client.
func newFakeNetBoxServer() *httptest.Server {
	devices := map[int]*fakeDevice{}
	nextID := 1

	mux := http.NewServeMux()

	// Classification endpoints: always resolve so UpsertDevice's create path
	// (site/role/manufacturer/device-type lookups) succeeds regardless of the
	// identity's vendor/model; conformance only cares about the identity
	// fields (name/serial/lifecycle_state/credential_ref/bmc_ip) round-tripping.
	mux.HandleFunc("/api/dcim/sites/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"id": 1}}})
	})
	mux.HandleFunc("/api/dcim/device-roles/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"id": 2}}})
	})
	mux.HandleFunc("/api/dcim/manufacturers/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"id": 3}}})
	})
	mux.HandleFunc("/api/dcim/device-types/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"id": 4}}})
	})

	mux.HandleFunc("/api/dcim/devices/", func(w http.ResponseWriter, r *http.Request) {
		sub := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/dcim/devices/"), "/")

		switch r.Method {
		case http.MethodGet:
			if sub == "" {
				q := r.URL.Query()
				if serial := q.Get("serial"); serial != "" {
					for _, d := range devices {
						if d.Serial == serial {
							_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"id": d.ID}}})
							return
						}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
					return
				}
				if name := q.Get("name"); name != "" {
					for _, d := range devices {
						if d.Name == name {
							_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"id": d.ID}}})
							return
						}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
					return
				}
				// Plain list (ListDevices): no query filters.
				results := make([]any, 0, len(devices))
				for _, d := range devices {
					results = append(results, fakeDeviceJSON(d))
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"count": len(results), "next": nil, "results": results,
				})
				return
			}
			id, err := strconv.Atoi(sub)
			if err != nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			d, ok := devices[id]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(fakeDeviceJSON(d))

		case http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			d := &fakeDevice{ID: nextID, CF: map[string]any{}}
			nextID++
			if name, ok := body["name"].(string); ok {
				d.Name = name
			}
			if serial, ok := body["serial"].(string); ok {
				d.Serial = serial
			}
			if cf, ok := body["custom_fields"].(map[string]any); ok {
				d.CF = cf
			}
			devices[d.ID] = d
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": d.ID})

		case http.MethodPatch:
			id, err := strconv.Atoi(sub)
			if err != nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			d, ok := devices[id]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if name, ok := body["name"].(string); ok {
				d.Name = name
			}
			if serial, ok := body["serial"].(string); ok {
				d.Serial = serial
			}
			if cf, ok := body["custom_fields"].(map[string]any); ok {
				for k, v := range cf {
					d.CF[k] = v
				}
			}
			w.WriteHeader(http.StatusOK)

		case http.MethodDelete:
			id, err := strconv.Atoi(sub)
			if err != nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if _, ok := devices[id]; !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			delete(devices, id)
			w.WriteHeader(http.StatusNoContent)

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	return httptest.NewServer(mux)
}

// TestClientDirectoryConformance proves *netbox.Client behaves like any
// other directory.Store: a fresh fake NetBox server (fresh in-memory device
// map) backs each newStore() call so every sub-test starts from empty state,
// same as the sibling FileStore's conformance run.
func TestClientDirectoryConformance(t *testing.T) {
	directory.RunConformance(t, func() directory.Store {
		srv := newFakeNetBoxServer()
		t.Cleanup(srv.Close)
		return netbox.New(srv.URL, "test-token")
	})
}
