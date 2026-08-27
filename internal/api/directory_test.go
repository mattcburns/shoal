package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattcburns/shoal/internal/api"
	"github.com/mattcburns/shoal/internal/common/config"
	"github.com/mattcburns/shoal/internal/common/directory"
	"github.com/mattcburns/shoal/internal/common/models"
)

func TestListDevicesWithoutStore(t *testing.T) {
	s := api.New(config.Config{}, nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/devices", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestListDevicesEmptyIsArrayNotNull(t *testing.T) {
	s := api.New(config.Config{}, nil).WithDirectory(directory.NewMemory())
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/devices", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["devices"] == nil {
		t.Fatal("devices must be [] not null")
	}
}

func TestCreateAndListDevices(t *testing.T) {
	store := directory.NewMemory()
	s := api.New(config.Config{}, nil).WithDirectory(store)

	payload, err := json.Marshal(map[string]string{
		"name":   "lab-node-1",
		"serial": "SN123",
		"bmc_ip": "10.0.0.5",
	})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/devices", bytes.NewReader(payload))
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	var created models.DeviceIdentity
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Fatal("want non-empty id in response")
	}
	if created.Name != "lab-node-1" || created.Serial != "SN123" || created.BMCIP != "10.0.0.5" {
		t.Fatalf("got %+v", created)
	}
	if created.LifecycleState != models.StateDiscovered {
		t.Fatalf("want default lifecycle_state discovered, got %q", created.LifecycleState)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/devices", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Devices []models.DeviceIdentity `json:"devices"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Devices) != 1 || body.Devices[0].ID != created.ID {
		t.Fatalf("want 1 device matching created id, got %+v", body.Devices)
	}
}

func TestCreateDeviceRejectsClientSetLifecycle(t *testing.T) {
	store := directory.NewMemory()
	s := api.New(config.Config{}, nil).WithDirectory(store)

	payload, err := json.Marshal(map[string]string{
		"name":            "lab-node-1",
		"lifecycle_state": "provisioned",
	})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/devices", bytes.NewReader(payload))
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	var created models.DeviceIdentity
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.LifecycleState != models.StateDiscovered {
		t.Fatalf("want server-set lifecycle_state discovered (client value ignored), got %q", created.LifecycleState)
	}
}

func TestCreateDeviceWithoutStore(t *testing.T) {
	s := api.New(config.Config{}, nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/devices", bytes.NewReader([]byte(`{"name":"x"}`)))
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateDeviceRequiresNameOrSerial(t *testing.T) {
	s := api.New(config.Config{}, nil).WithDirectory(directory.NewMemory())
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/devices", bytes.NewReader([]byte(`{"vendor":"dell"}`)))
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateDeviceInvalidJSON(t *testing.T) {
	s := api.New(config.Config{}, nil).WithDirectory(directory.NewMemory())
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/devices", bytes.NewReader([]byte(`not json`)))
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
}
