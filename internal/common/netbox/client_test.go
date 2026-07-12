package netbox_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
