package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattcburns/shoal/internal/common/directory"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	store, err := directory.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return New(Config{Directory: store, Log: testLogger()})
}

func TestDeviceListEmpty(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/ui/devices", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "No devices yet") {
		t.Fatalf("body missing empty-state text: %s", rec.Body.String())
	}
}

func TestDeviceAddListEditDeleteFlow(t *testing.T) {
	s := newTestServer(t)

	// Add.
	addReq := httptest.NewRequest(http.MethodPost, "/ui/devices/new",
		strings.NewReader("name=lab-node-1&serial=SN1&bmc_ip=10.0.0.5"))
	addReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(addRec, addReq)
	if addRec.Code != http.StatusFound {
		t.Fatalf("add: got %d, want 302; body=%s", addRec.Code, addRec.Body.String())
	}
	loc := addRec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/ui/devices/") {
		t.Fatalf("add: Location = %q", loc)
	}
	id := strings.TrimPrefix(loc, "/ui/devices/")

	// Appears in the list.
	listReq := httptest.NewRequest(http.MethodGet, "/ui/devices", nil)
	listRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(listRec, listReq)
	if !strings.Contains(listRec.Body.String(), "lab-node-1") {
		t.Fatalf("list missing new device: %s", listRec.Body.String())
	}

	// Detail page.
	detailReq := httptest.NewRequest(http.MethodGet, "/ui/devices/"+id, nil)
	detailRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(detailRec, detailReq)
	if detailRec.Code != http.StatusOK || !strings.Contains(detailRec.Body.String(), "SN1") {
		t.Fatalf("detail: got %d, body=%s", detailRec.Code, detailRec.Body.String())
	}

	// Edit.
	editReq := httptest.NewRequest(http.MethodPost, "/ui/devices/"+id+"/edit",
		strings.NewReader("name=lab-node-1-renamed&serial=SN1&bmc_ip=10.0.0.6"))
	editReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	editRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(editRec, editReq)
	if editRec.Code != http.StatusFound {
		t.Fatalf("edit: got %d, want 302; body=%s", editRec.Code, editRec.Body.String())
	}

	detailReq2 := httptest.NewRequest(http.MethodGet, "/ui/devices/"+id, nil)
	detailRec2 := httptest.NewRecorder()
	s.Handler().ServeHTTP(detailRec2, detailReq2)
	if !strings.Contains(detailRec2.Body.String(), "lab-node-1-renamed") {
		t.Fatalf("detail after edit missing new name: %s", detailRec2.Body.String())
	}
	if !strings.Contains(detailRec2.Body.String(), "10.0.0.6") {
		t.Fatalf("detail after edit missing new bmc_ip: %s", detailRec2.Body.String())
	}

	// Delete.
	delReq := httptest.NewRequest(http.MethodPost, "/ui/devices/"+id+"/delete", nil)
	delRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusFound {
		t.Fatalf("delete: got %d, want 302", delRec.Code)
	}

	// The device detail route is the Status/Provision tab (status.go), which
	// deliberately renders 200 with an empty/no-data state for an id it
	// doesn't recognize -- e.g. so an operator can still start a job for a
	// device the directory hasn't seen yet -- rather than 404ing, mirroring
	// this repo's existing "spike jobs without NetBox rows still start"
	// pattern. It must not still show the just-deleted device's identity.
	getReq := httptest.NewRequest(http.MethodGet, "/ui/devices/"+id, nil)
	getRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get after delete: got %d, want 200 (empty status page)", getRec.Code)
	}
	if strings.Contains(getRec.Body.String(), "lab-node-1-renamed") {
		t.Fatalf("get after delete: still shows deleted device's identity: %s", getRec.Body.String())
	}
}

func TestDeviceNewRequiresName(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/ui/devices/new", strings.NewReader("serial=SN1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (re-render form with error)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "name is required") {
		t.Fatalf("body missing validation error: %s", rec.Body.String())
	}
}

// TestDeviceDetailUnknownIDRendersEmptyState: the detail route is the
// Status/Provision tab (status.go's registerStatusRoutes), which
// intentionally renders 200 for an id the directory doesn't recognize
// (rather than 404) so an operator can still start a job for a device the
// directory hasn't seen yet.
func TestDeviceDetailUnknownIDRendersEmptyState(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/ui/devices/does-not-exist", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (empty status page)", rec.Code)
	}
}

func TestDeviceMutationsRequireCSRFWhenAuthEnabled(t *testing.T) {
	const token = "s3cr3t-token"
	store, err := directory.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	s := New(Config{Directory: store, APIToken: token, Log: testLogger()})

	// Log in to obtain a valid session cookie.
	loginReq := httptest.NewRequest(http.MethodPost, "/ui/login", strings.NewReader("password="+token))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(loginRec, loginReq)
	var sessionCookie *http.Cookie
	for _, c := range loginRec.Result().Cookies() {
		if c.Name == sessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected a session cookie from login")
	}

	// GET the new-device form to read the CSRF token it embeds.
	formReq := httptest.NewRequest(http.MethodGet, "/ui/devices/new", nil)
	formReq.AddCookie(sessionCookie)
	formRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(formRec, formReq)
	body := formRec.Body.String()
	const marker = `name="csrf_token" value="`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("form missing csrf_token field: %s", body)
	}
	i += len(marker)
	j := strings.Index(body[i:], `"`)
	if j < 0 {
		t.Fatalf("malformed csrf_token field: %s", body)
	}
	validToken := body[i : i+j]

	post := func(token string) int {
		req := httptest.NewRequest(http.MethodPost, "/ui/devices/new",
			strings.NewReader("name=lab-node-1&csrf_token="+token))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(sessionCookie)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec.Code
	}

	if code := post("wrong-token"); code != http.StatusOK {
		t.Fatalf("wrong csrf_token: got %d, want 200 (re-rendered form with error)", code)
	}
	if code := post(""); code != http.StatusOK {
		t.Fatalf("missing csrf_token: got %d, want 200 (re-rendered form with error)", code)
	}
	if code := post(validToken); code != http.StatusFound {
		t.Fatalf("valid csrf_token: got %d, want 302", code)
	}
}

func TestDeviceMutationsSkipCSRFWhenAuthDisabled(t *testing.T) {
	// newTestServer's Config carries no APIToken, so CSRF is a no-op --
	// covered by every other TestDeviceAdd*/Edit*/Delete test in this file.
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/ui/devices/new",
		strings.NewReader("name=no-csrf-needed"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("got %d, want 302 (no csrf_token required when auth disabled)", rec.Code)
	}
}

func TestRootRedirectsToDevices(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/devices" {
		t.Fatalf("got %d %q, want 302 /ui/devices", rec.Code, rec.Header().Get("Location"))
	}
}
