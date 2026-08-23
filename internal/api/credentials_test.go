package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattcburns/shoal/internal/common/config"
)

type fakeCreds struct {
	view DeviceCredentialsView
	put  DeviceCredentialsPut
	err  error
	u, p string
}

func (f *fakeCreds) Get(_ context.Context, deviceID, _ string) (DeviceCredentialsView, error) {
	if f.err != nil {
		return DeviceCredentialsView{}, f.err
	}
	v := f.view
	v.DeviceID = deviceID
	return v, nil
}

func (f *fakeCreds) Put(_ context.Context, deviceID string, req DeviceCredentialsPut) (DeviceCredentialsView, error) {
	f.put = req
	if f.err != nil {
		return DeviceCredentialsView{}, f.err
	}
	return DeviceCredentialsView{
		DeviceID:      deviceID,
		CredentialRef: "bmc-x",
		Username:      req.Username,
		HasPassword:   req.Password != "" || f.view.HasPassword,
	}, nil
}

func (f *fakeCreds) Resolve(_ context.Context, _ string) (string, string, error) {
	return f.u, f.p, f.err
}

func TestDeviceCredentialsGetPut(t *testing.T) {
	fc := &fakeCreds{view: DeviceCredentialsView{Username: "root", HasPassword: true, CredentialRef: "bmc-C784MH3"}}
	s := New(config.Config{}, nil).WithDeviceCredentials(fc)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/devices/6/credentials", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status %d body=%s", rr.Code, rr.Body.String())
	}
	var got DeviceCredentialsView
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Username != "root" || !got.HasPassword || got.CredentialRef != "bmc-C784MH3" {
		t.Fatalf("%+v", got)
	}
	if bytes.Contains(rr.Body.Bytes(), []byte("password")) && bytes.Contains(rr.Body.Bytes(), []byte(`"password":`)) {
		t.Fatal("GET must not include password field")
	}

	body, _ := json.Marshal(DeviceCredentialsPut{Username: "root", Password: "secret"})
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/v1/devices/6/credentials", bytes.NewReader(body))
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status %d body=%s", rr.Code, rr.Body.String())
	}
	if fc.put.Username != "root" || fc.put.Password != "secret" {
		t.Fatalf("put %+v", fc.put)
	}
}

func TestDeviceCredentialsPutRequiresUsername(t *testing.T) {
	s := New(config.Config{}, nil).WithDeviceCredentials(&fakeCreds{})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/v1/devices/6/credentials", bytes.NewReader([]byte(`{"password":"x"}`)))
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestDeviceCredentialsGetPassesCredentialRef(t *testing.T) {
	fc := &fakeCreds{view: DeviceCredentialsView{Username: "root", HasPassword: true, CredentialRef: "bmc-C784MH3"}}
	s := New(config.Config{}, nil).WithDeviceCredentials(fc)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/devices/6/credentials?credential_ref=bmc-C784MH3", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestDeviceCredentialsGetEmptyOK(t *testing.T) {
	s := New(config.Config{}, nil).WithDeviceCredentials(&fakeCreds{})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/devices/6/credentials", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	if bytes.Contains(rr.Body.Bytes(), []byte(`"password":`)) {
		t.Fatal("GET must not include password field")
	}
}

func TestDeviceCredentialsUnavailable(t *testing.T) {
	s := New(config.Config{}, nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/devices/6/credentials", nil)
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestDevicePowerUsesStoredCredentials(t *testing.T) {
	fp := &fakePower{}
	fc := &fakeCreds{u: "idrac", p: "stored"}
	s := New(config.Config{BMCUsername: "env", BMCPassword: "envpass"}, nil).
		WithDeviceCredentials(fc).WithDevicePower(fp)
	body, _ := json.Marshal(DevicePowerRequest{ResetType: "On", BMCEndpoint: "https://bmc"})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/devices/6/power", bytes.NewReader(body))
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	if fp.lastReq.BMCUsername != "idrac" || fp.lastReq.BMCPassword != "stored" {
		t.Fatalf("want stored creds, got %+v", fp.lastReq)
	}
}
