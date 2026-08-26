package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattcburns/shoal/internal/common/config"
)

// errBackendDetail stands in for a raw upstream error that must never reach
// an API client verbatim (e.g. a BMC hostname or secrets-backend path).
var errBackendDetail = errors.New("dial tcp 10.9.8.7:5432: connection refused")

// assertUpstreamError checks the consistent 502 response used for "a
// configured backend dependency failed": a fixed status code and a body
// that never contains the raw underlying error text.
func assertUpstreamError(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "10.9.8.7") {
		t.Fatalf("response leaked raw internal error detail: %s", rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["error"]; !ok {
		t.Fatalf("missing error field: %s", rr.Body.String())
	}
}

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

func TestDevicePollUpstreamError(t *testing.T) {
	fp := &fakePoll{err: errBackendDetail, out: DevicePollResult{SELNew: 2}}
	s := New(config.Config{}, nil).WithDevicePoll(fp)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/devices/6/poll", strings.NewReader(`{"bmc_endpoint":"https://bmc"}`))
	s.Handler().ServeHTTP(rr, req)
	assertUpstreamError(t, rr)
	// Partial poll results gathered before the failure are still useful to
	// the caller and are not raw error detail, so they remain in the body.
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if sel, ok := body["sel_new"].(float64); !ok || sel != 2 {
		t.Fatalf("expected partial results preserved, got %+v", body)
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
