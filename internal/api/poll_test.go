package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattcburns/shoal/internal/common/config"
)

type fakePoll struct {
	lastID  string
	lastReq DevicePollRequest
	err     error
	out     DevicePollResult
}

func (f *fakePoll) Poll(_ context.Context, deviceID string, req DevicePollRequest) (DevicePollResult, error) {
	f.lastID = deviceID
	f.lastReq = req
	if f.err != nil {
		out := f.out
		out.DeviceID = deviceID
		return out, f.err
	}
	out := f.out
	if out.DeviceID == "" {
		out.DeviceID = deviceID
	}
	return out, nil
}

func TestDevicePollSuccess(t *testing.T) {
	fp := &fakePoll{out: DevicePollResult{SELNew: 1, SensorsWritten: 26}}
	s := New(config.Config{BMCUsername: "env", BMCPassword: "envpass"}, nil).WithDevicePoll(fp)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/devices/6/poll", strings.NewReader(`{"bmc_endpoint":"https://172.16.21.202"}`))
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	if fp.lastID != "6" || fp.lastReq.BMCEndpoint != "https://172.16.21.202" {
		t.Fatalf("%+v", fp.lastReq)
	}
	if fp.lastReq.BMCUsername != "env" || fp.lastReq.BMCPassword != "envpass" {
		t.Fatal("expected env BMC defaults when body omits creds")
	}
	var got DevicePollResult
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.SensorsWritten != 26 || got.SELNew != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestDevicePollFillsEndpointAndCreds(t *testing.T) {
	fp := &fakePoll{out: DevicePollResult{SensorsWritten: 3}}
	fc := &fakeCreds{view: DeviceCredentialsView{BMCIP: "172.16.21.202"}, u: "root", p: "secret"}
	s := New(config.Config{}, nil).WithDeviceCredentials(fc).WithDevicePoll(fp)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/devices/6/poll", strings.NewReader(`{}`))
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	if fp.lastReq.BMCEndpoint != "https://172.16.21.202" {
		t.Fatalf("endpoint %+v", fp.lastReq)
	}
	if fp.lastReq.BMCUsername != "root" || fp.lastReq.BMCPassword != "secret" {
		t.Fatalf("creds %+v", fp.lastReq)
	}
}

func TestDevicePollUnavailable(t *testing.T) {
	s := New(config.Config{}, nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/devices/6/poll", strings.NewReader(`{"bmc_endpoint":"https://bmc"}`))
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestDevicePollRejectsMissingEndpoint(t *testing.T) {
	s := New(config.Config{}, nil).WithDevicePoll(&fakePoll{})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/devices/6/poll", strings.NewReader(`{}`))
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rr.Code)
	}
}
