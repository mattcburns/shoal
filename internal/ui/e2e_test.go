package ui_test

// This is the end-to-end exercise for the Status/Provision/Power/Credentials
// tab described in this unit's task. The task's E2E recipe assumes `shoal
// serve` already mounts /ui/* (via the sibling "UI shell" unit's cli.go
// wiring), which does not exist in this worktree yet -- internal/cli is
// off-limits for this unit and owned by that sibling PR. So this test drives
// the same routes (login redirect, GET status page, POST start/cancel/
// deprovision/credentials/power) directly against ui.Server's own
// httptest.Server, with fakes standing in for JobStarter/JobCanceler/
// DeviceCredentials/DevicePower/profile.Store/DeviceDirectory. Once the shell
// PR lands and mounts this package under a real `shoal serve`, the curl-based
// recipe in the task description exercises the identical handlers.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/mattcburns/shoal/internal/api"
	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/core/profile"
	"github.com/mattcburns/shoal/internal/ui"
)

// fakeDirectory implements the full directory.Store interface -- only
// GetDevice is exercised by this test, the rest are unreachable stubs.
type fakeDirectory struct{ dev models.DeviceIdentity }

func (f fakeDirectory) GetDevice(_ context.Context, _ string) (models.DeviceIdentity, error) {
	return f.dev, nil
}

func (f fakeDirectory) ListDevices(_ context.Context) ([]models.DeviceIdentity, error) {
	return []models.DeviceIdentity{f.dev}, nil
}

func (f fakeDirectory) UpsertDevice(_ context.Context, d models.DeviceIdentity) (string, error) {
	return d.ID, nil
}

func (f fakeDirectory) SetLifecycle(_ context.Context, _ string, _ models.LifecycleState) error {
	return nil
}

func (f fakeDirectory) ResolveDeviceID(_ context.Context, key string) (string, error) {
	return key, nil
}

func (f fakeDirectory) DeleteDevice(_ context.Context, _ string) error {
	return nil
}

type fakeStarter struct {
	lastReq models.StartJobRequest
	err     error
	job     models.Job
}

func (f *fakeStarter) StartAsync(_ context.Context, req models.StartJobRequest) (models.Job, error) {
	f.lastReq = req
	if f.err != nil {
		return f.job, f.err
	}
	return models.Job{ID: "job-123", DeviceID: req.DeviceID, State: models.StateProvisioning}, nil
}

type fakeCreds struct{}

func (fakeCreds) Get(_ context.Context, deviceID, _ string) (api.DeviceCredentialsView, error) {
	return api.DeviceCredentialsView{DeviceID: deviceID, Username: "admin", HasPassword: true, BMCIP: "10.0.0.5"}, nil
}

func (fakeCreds) Put(_ context.Context, deviceID string, req api.DeviceCredentialsPut) (api.DeviceCredentialsView, error) {
	return api.DeviceCredentialsView{DeviceID: deviceID, CredentialRef: "bmc-x", Username: req.Username, HasPassword: true}, nil
}

func (fakeCreds) Resolve(_ context.Context, _ string) (string, string, error) {
	return "admin", "secret", nil
}

type fakePower struct{ err error }

func (f fakePower) Power(_ context.Context, deviceID string, req api.DevicePowerRequest) (api.DevicePowerResult, error) {
	if f.err != nil {
		return api.DevicePowerResult{}, f.err
	}
	return api.DevicePowerResult{DeviceID: deviceID, ResetType: req.ResetType, PowerState: "On"}, nil
}

func TestStatusTabE2E(t *testing.T) {
	// job.StartJobRequest requires prep_iso_url when prep=wipe_only unless
	// SHOAL_PREP_ISO_URL is set; the deprovision form (like the NetBox
	// plugin's) has no prep_iso_url field, so real deployments rely on this
	// env default (docs/design/deprovision-design.md).
	t.Setenv("SHOAL_PREP_ISO_URL", "http://example/prep.iso")

	profStore, err := profile.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	starter := &fakeStarter{}
	canceler := &fakeCanceler{}

	s := ui.New(ui.Config{
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		APIToken: "test-token",
		Directory: fakeDirectory{dev: models.DeviceIdentity{
			ID: "dev1", Name: "shoal-node-1", BMCIP: "192.168.1.50", CredentialRef: "bmc-dev1",
		}},
		Profiles:    profStore,
		JobStarter:  starter,
		JobCanceler: canceler,
		Credentials: fakeCreds{},
		Power:       fakePower{},
	})

	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	// Unauthenticated request -> redirected to login.
	resp, err := client.Get(ts.URL + "/ui/devices/dev1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Request.URL.Path != "/ui/login" {
		t.Fatalf("expected redirect to login, got %s", resp.Request.URL.Path)
	}

	// Log in.
	resp, err = client.PostForm(ts.URL+"/ui/login", url.Values{"password": {"test-token"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// GET status page: 200, contains the documented form field/button names.
	resp, err = client.Get(ts.URL + "/ui/devices/dev1")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	bs := string(body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status page: %d body=%s", resp.StatusCode, bs)
	}
	for _, want := range []string{
		`name="profile_ref"`, `name="wipe_level"`, `name="approve_destruct"`,
		"Start provision", "Deprovision", "BMC credentials", "Power on", "Force off", "Reset",
	} {
		if !strings.Contains(bs, want) {
			t.Errorf("status page missing %q", want)
		}
	}

	// POST start job.
	resp, err = client.PostForm(ts.URL+"/ui/devices/dev1", url.Values{
		"action":        {"start"},
		"bmc_endpoint":  {"https://192.168.1.50"},
		"iso_url":       {"http://example/iso"},
		"profile_ref":   {"spike"},
		"serial_target": {"shoal-node-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "started") {
		t.Fatalf("start job: %d body=%s", resp.StatusCode, string(body))
	}
	if starter.lastReq.DeviceID != "dev1" || starter.lastReq.BMCEndpoint != "https://192.168.1.50" {
		t.Fatalf("unexpected start req: %+v", starter.lastReq)
	}

	// POST cancel.
	resp, err = client.PostForm(ts.URL+"/ui/devices/dev1", url.Values{
		"action": {"cancel"},
		"job_id": {"job-123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if canceler.calledWith != "job-123" {
		t.Fatalf("cancel not called with expected id: %q", canceler.calledWith)
	}

	// POST deprovision without approve_destruct -> rejected server-side even
	// though the client-side confirm() would normally gate the submit.
	resp, err = client.PostForm(ts.URL+"/ui/devices/dev1", url.Values{
		"action":       {"deprovision"},
		"bmc_endpoint": {"https://192.168.1.50"},
		"wipe_level":   {"discard"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "confirmation is required") {
		t.Fatalf("expected confirmation-required flash, got: %s", string(body))
	}

	// POST deprovision with approval.
	resp, err = client.PostForm(ts.URL+"/ui/devices/dev1", url.Values{
		"action":           {"deprovision"},
		"bmc_endpoint":     {"https://192.168.1.50"},
		"wipe_level":       {"discard"},
		"approve_destruct": {"on"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Deprovision job") {
		t.Fatalf("expected deprovision started, got: %s", string(body))
	}

	// POST credentials.
	resp, err = client.PostForm(ts.URL+"/ui/devices/dev1", url.Values{
		"action":       {"credentials"},
		"bmc_username": {"admin"},
		"bmc_password": {"newpass"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "BMC credentials saved") {
		t.Fatalf("expected credentials saved, got: %s", string(body))
	}

	// POST power on.
	resp, err = client.PostForm(ts.URL+"/ui/devices/dev1", url.Values{
		"action":       {"power"},
		"reset_type":   {"On"},
		"bmc_endpoint": {"https://192.168.1.50"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Power On sent") {
		t.Fatalf("expected power sent, got: %s", string(body))
	}

	// An upstream failure (e.g. an unreachable BMC) must render a flash, not
	// a 500 or a leaked raw error string.
	s.Power = fakePower{err: errUnreachable}
	resp, err = client.PostForm(ts.URL+"/ui/devices/dev1", url.Values{
		"action":       {"power"},
		"reset_type":   {"On"},
		"bmc_endpoint": {"https://10.255.255.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		t.Fatalf("upstream failure should not 500: %d body=%s", resp.StatusCode, string(body))
	}
	if strings.Contains(string(body), errUnreachable.Error()) {
		t.Fatalf("raw upstream error leaked to client: %s", string(body))
	}
	if !strings.Contains(string(body), "upstream request failed") {
		t.Fatalf("expected generic upstream-error flash, got: %s", string(body))
	}
}

type staticErr string

func (e staticErr) Error() string { return string(e) }

const errUnreachable = staticErr("dial tcp 10.255.255.1:443: i/o timeout: raw-bmc-detail-should-not-leak")
