package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattcburns/shoal/internal/common/config"
)

type fakePower struct {
	lastID  string
	lastReq DevicePowerRequest
	err     error
	out     DevicePowerResult
}

func (f *fakePower) Power(_ context.Context, deviceID string, req DevicePowerRequest) (DevicePowerResult, error) {
	f.lastID = deviceID
	f.lastReq = req
	if f.err != nil {
		return DevicePowerResult{}, f.err
	}
	out := f.out
	if out.DeviceID == "" {
		out.DeviceID = deviceID
	}
	if out.ResetType == "" {
		out.ResetType = req.ResetType
	}
	return out, nil
}

func TestDevicePowerSuccess(t *testing.T) {
	fp := &fakePower{out: DevicePowerResult{PowerState: "On", ResetType: "On"}}
	s := New(config.Config{BMCUsername: "root", BMCPassword: "lab"}, nil).WithDevicePower(fp)
	body, _ := json.Marshal(DevicePowerRequest{
		ResetType: "On", BMCEndpoint: "https://172.16.21.202",
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/devices/6/power", bytes.NewReader(body))
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	if fp.lastID != "6" || fp.lastReq.ResetType != "On" {
		t.Fatalf("%+v", fp.lastReq)
	}
	if fp.lastReq.BMCUsername != "root" || fp.lastReq.BMCPassword != "lab" {
		t.Fatal("expected env BMC defaults when body omits creds")
	}
	var got DevicePowerResult
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.PowerState != "On" {
		t.Fatalf("%+v", got)
	}
}

func TestDevicePowerRejectsBadType(t *testing.T) {
	s := New(config.Config{}, nil).WithDevicePower(&fakePower{})
	body := []byte(`{"reset_type":"Explode","bmc_endpoint":"https://bmc"}`)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/devices/6/power", bytes.NewReader(body))
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestDevicePowerFillsEndpointFromStoredBMCIP(t *testing.T) {
	fp := &fakePower{out: DevicePowerResult{PowerState: "Off", ResetType: "On"}}
	fc := &fakeCreds{view: DeviceCredentialsView{BMCIP: "172.16.21.202"}, u: "root", p: "x"}
	s := New(config.Config{}, nil).WithDeviceCredentials(fc).WithDevicePower(fp)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/devices/6/power", strings.NewReader(`{"reset_type":"On"}`))
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	if fp.lastReq.BMCEndpoint != "https://172.16.21.202" {
		t.Fatalf("endpoint %+v", fp.lastReq)
	}
}

func TestDevicePowerUpstreamError(t *testing.T) {
	fp := &fakePower{err: errBackendDetail}
	s := New(config.Config{}, nil).WithDevicePower(fp)
	body, _ := json.Marshal(DevicePowerRequest{ResetType: "On", BMCEndpoint: "https://bmc"})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/devices/6/power", bytes.NewReader(body))
	s.Handler().ServeHTTP(rr, req)
	assertUpstreamError(t, rr)
}

func TestDevicePowerUnavailable(t *testing.T) {
	s := New(config.Config{}, nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/devices/6/power", strings.NewReader(`{"reset_type":"On","bmc_endpoint":"https://bmc"}`))
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", rr.Code)
	}
}
