package ui_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/telemetry"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
	"github.com/mattcburns/shoal/internal/observe"
	"github.com/mattcburns/shoal/internal/ui"
)

func TestJobsEmptyState(t *testing.T) {
	store := jobstore.NewMemory()
	telem := telemetry.NewMemory()
	obs := observe.New(testLog(), store, telem, nil)
	srv := ui.New(testLog())
	srv.Observe = obs
	srv.Jobs = store

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/devices/dev-1/jobs", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"ID", "State", "Phase", "Progress", "Stages", "Profile", "Updated", "Error", "No jobs recorded", "No job selected for log view"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q; body=%s", want, body)
		}
	}
}

func TestJobsStoreNotConfigured(t *testing.T) {
	srv := ui.New(testLog())
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/devices/dev-1/jobs", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "job store not configured") {
		t.Errorf("body missing not-configured banner; body=%s", rr.Body.String())
	}
}

type fakeCanceler struct {
	calledWith string
	err        error
}

func (f *fakeCanceler) Cancel(_ context.Context, jobID string) error {
	f.calledWith = jobID
	return f.err
}

func percentPtr(n int) *int { return &n }

func TestJobsActiveJobLogAndCancel(t *testing.T) {
	store := jobstore.NewMemory()
	telem := telemetry.NewMemory()
	obs := observe.New(testLog(), store, telem, nil)
	ctx := context.Background()

	now := time.Now().UTC()
	job := models.Job{
		ID:         "job-123",
		DeviceID:   "dev-1",
		ProfileRef: "spike",
		State:      models.StateProvisioning,
		Phase:      "install",
		Percent:    percentPtr(42),
		UpdatedAt:  &now,
		Stages: []models.JobStage{
			{ID: "s1", Kind: "prep", State: "done"},
			{ID: "s2", Kind: "os_install", State: "running", Phase: "install"},
		},
	}
	if err := store.Insert(ctx, job); err != nil {
		t.Fatalf("insert job: %v", err)
	}
	if err := telem.WriteJobLog(ctx, "job-123", now, "SHOAL|1|1|-|install|-"); err != nil {
		t.Fatalf("seed job log: %v", err)
	}

	canceler := &fakeCanceler{}
	srv := ui.New(testLog())
	srv.Observe = obs
	srv.Jobs = store
	srv.Canceler = canceler

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/devices/dev-1/jobs", nil)
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"job-123", "provisioning", "install", "42%", "os_install", "spike",
		"SHOAL|1|1|-|install|-", "Cancel active job",
		`<meta http-equiv="refresh" content="5">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q; body=%s", want, body)
		}
	}

	// Cancel POST redirects back to the jobs tab and calls Canceler.Cancel.
	form := url.Values{"job_id": {"job-123"}}
	cancelReq := httptest.NewRequest(http.MethodPost, "/ui/devices/dev-1/jobs", strings.NewReader(form.Encode()))
	cancelReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	cancelRR := httptest.NewRecorder()
	srv.Handler().ServeHTTP(cancelRR, cancelReq)

	if cancelRR.Code != http.StatusSeeOther {
		t.Fatalf("cancel status = %d", cancelRR.Code)
	}
	if loc := cancelRR.Header().Get("Location"); loc != "/ui/devices/dev-1/jobs" {
		t.Errorf("redirect location = %q", loc)
	}
	if canceler.calledWith != "job-123" {
		t.Errorf("Cancel called with %q, want job-123", canceler.calledWith)
	}
}

func TestJobsCancelNotFoundSurfacesBanner(t *testing.T) {
	store := jobstore.NewMemory()
	srv := ui.New(testLog())
	srv.Jobs = store
	srv.Canceler = &fakeCanceler{err: jobstore.ErrNotFound}

	form := url.Values{"job_id": {"missing-job"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/devices/dev-1/jobs", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if loc != "/ui/devices/dev-1/jobs?cancel_error=not_found" {
		t.Fatalf("redirect location = %q", loc)
	}

	// Following the redirect renders a banner, never the raw error text.
	followReq := httptest.NewRequest(http.MethodGet, loc, nil)
	followRR := httptest.NewRecorder()
	srv.Handler().ServeHTTP(followRR, followReq)
	if followRR.Code != http.StatusOK {
		t.Fatalf("follow status = %d", followRR.Code)
	}
	body := followRR.Body.String()
	if !strings.Contains(body, "Cancel failed: job not found.") {
		t.Errorf("body missing cancel-error banner; body=%s", body)
	}
	if strings.Contains(body, jobstore.ErrNotFound.Error()) {
		t.Errorf("body leaked raw error text")
	}
}

func TestJobsLimitClampedTo200(t *testing.T) {
	store := jobstore.NewMemory()
	obs := observe.New(testLog(), store, telemetry.NewMemory(), nil)
	srv := ui.New(testLog())
	srv.Observe = obs
	srv.Jobs = store

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/devices/dev-1/jobs?limit="+strconv.Itoa(10000), nil)
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
}
