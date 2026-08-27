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
	ID           int
	Name         string
	Serial       string
	DeviceTypeID int
	CF           map[string]any
}

// fakeManufacturer/fakeDeviceType back the classification lookups
// UpsertDevice's create path performs (ensureManufacturer/ensureDeviceType
// in client.go) -- tracked for real, rather than short-circuited to a fixed
// id, so a device's Vendor/Model round-trip through GetDevice/ListDevices
// exactly as directory.RunConformance requires.
type fakeManufacturer struct {
	ID   int
	Name string
}
type fakeDeviceType struct {
	ID             int
	Model          string
	ManufacturerID int
}

func fakeDeviceJSON(d *fakeDevice, mfgs map[int]*fakeManufacturer, types map[int]*fakeDeviceType) map[string]any {
	model, mfgName := "", ""
	if dt, ok := types[d.DeviceTypeID]; ok {
		model = dt.Model
		if mfg, ok := mfgs[dt.ManufacturerID]; ok {
			mfgName = mfg.Name
		}
	}
	return map[string]any{
		"id":            d.ID,
		"name":          d.Name,
		"serial":        d.Serial,
		"custom_fields": d.CF,
		"device_type": map[string]any{
			"model":        model,
			"manufacturer": map[string]any{"name": mfgName},
		},
	}
}

// newFakeNetBoxServer builds an in-memory httptest fake of the NetBox DCIM
// device/manufacturer/device-type endpoints (list/get/create/update/delete,
// plus the classification find-or-create lookups UpsertDevice's create path
// needs) sufficient to drive directory.RunConformance end to end against a
// real *netbox.Client, including a faithful Vendor/Model round-trip.
func newFakeNetBoxServer() *httptest.Server {
	devices := map[int]*fakeDevice{}
	nextDeviceID := 1
	mfgs := map[int]*fakeManufacturer{}
	nextMfgID := 1
	types := map[int]*fakeDeviceType{}
	nextTypeID := 1

	mux := http.NewServeMux()

	mux.HandleFunc("/api/dcim/sites/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"id": 1}}})
	})
	mux.HandleFunc("/api/dcim/device-roles/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"id": 2}}})
		case http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 2})
		}
	})

	mux.HandleFunc("/api/dcim/manufacturers/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			name := r.URL.Query().Get("name")
			for _, m := range mfgs {
				if m.Name == name {
					_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"id": m.ID}}})
					return
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
		case http.MethodPost:
			var body struct {
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			m := &fakeManufacturer{ID: nextMfgID, Name: body.Name}
			mfgs[m.ID] = m
			nextMfgID++
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": m.ID})
		}
	})

	mux.HandleFunc("/api/dcim/device-types/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			model := r.URL.Query().Get("model")
			mfgID, _ := strconv.Atoi(r.URL.Query().Get("manufacturer_id"))
			for _, t := range types {
				if t.Model == model && t.ManufacturerID == mfgID {
					_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"id": t.ID}}})
					return
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
		case http.MethodPost:
			var body struct {
				Manufacturer int    `json:"manufacturer"`
				Model        string `json:"model"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			t := &fakeDeviceType{ID: nextTypeID, Model: body.Model, ManufacturerID: body.Manufacturer}
			types[t.ID] = t
			nextTypeID++
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": t.ID})
		}
	})

	applyDeviceTypeField := func(d *fakeDevice, body map[string]any) {
		switch v := body["device_type"].(type) {
		case float64:
			d.DeviceTypeID = int(v)
		case json.Number:
			n, _ := v.Int64()
			d.DeviceTypeID = int(n)
		}
	}

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
					results = append(results, fakeDeviceJSON(d, mfgs, types))
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
			_ = json.NewEncoder(w).Encode(fakeDeviceJSON(d, mfgs, types))

		case http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			d := &fakeDevice{ID: nextDeviceID, CF: map[string]any{}}
			nextDeviceID++
			if name, ok := body["name"].(string); ok {
				d.Name = name
			}
			if serial, ok := body["serial"].(string); ok {
				d.Serial = serial
			}
			if cf, ok := body["custom_fields"].(map[string]any); ok {
				d.CF = cf
			}
			applyDeviceTypeField(d, body)
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
			applyDeviceTypeField(d, body)
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
// other directory.Store: a fresh fake NetBox server (fresh in-memory device/
// manufacturer/device-type maps) backs each newStore() call so every
// sub-test starts from empty state, same as the sibling FileStore's
// conformance run.
func TestClientDirectoryConformance(t *testing.T) {
	directory.RunConformance(t, func() directory.Store {
		srv := newFakeNetBoxServer()
		t.Cleanup(srv.Close)
		return netbox.New(srv.URL, "test-token")
	})
}
