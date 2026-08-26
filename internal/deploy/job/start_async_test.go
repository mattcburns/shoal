package job_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/common/secrets"
	"github.com/mattcburns/shoal/internal/deploy/job"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
	"github.com/mattcburns/shoal/internal/observe/sol"
)

// gatedBMC wraps the Fake so a test can hold BMC bring-up open (or fail it) at
// the media-insert step, standing in for the ~40s a real iDRAC takes.
type gatedBMC struct {
	*redfish.Fake
	release   chan struct{} // insert blocks until closed
	insertErr error
	mu        sync.Mutex
	inserted  bool
}

func (g *gatedBMC) InsertVirtualMedia(ctx context.Context, mediaURI, imageURL string) error {
	if g.release != nil {
		select {
		case <-g.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if g.insertErr != nil {
		return g.insertErr
	}
	g.mu.Lock()
	g.inserted = true
	g.mu.Unlock()
	return g.Fake.InsertVirtualMedia(ctx, mediaURI, imageURL)
}

func (g *gatedBMC) didInsert() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inserted
}

func newAsyncOrch(t *testing.T, bmc redfish.BMC) (*job.Orchestrator, jobstore.Store) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := jobstore.NewMemory()
	watch := sol.NewWatchService(log, nil)
	pr, _ := io.Pipe()
	watch.NewTransport = func(models.WatchSession) sol.Transport {
		return sol.NewReaderTransport(pr)
	}
	orch := job.NewOrchestrator(job.Options{
		Log:     log,
		Store:   store,
		Secrets: secrets.NewMemory(),
		NewBMC: func(redfish.Config) (redfish.BMC, error) {
			return bmc, nil
		},
		Watches:  watch,
		AuthMode: "basic",
		TLSMode:  "off",
	})
	t.Cleanup(orch.Stop)
	watch.SetProgress(orch.ProgressPort())
	return orch, store
}

func asyncReq() models.StartJobRequest {
	return models.StartJobRequest{
		DeviceID:     "node-1",
		SerialTarget: "node-1",
		BMCEndpoint:  "http://bmc.test",
		BMCUsername:  "admin",
		BMCPassword:  "secret",
		ISOURL:       "http://iso/shoal-marker.iso",
	}
}

// TestStartAsyncReturnsBeforeBringUpCompletes is the contract POST /v1/jobs
// depends on: the call returns a durable, immediately-pollable job while BMC
// bring-up is still in flight. Blocking until bring-up finished (~40s on a real
// R750) is what made HTTP clients time out on jobs that were starting fine.
func TestStartAsyncReturnsBeforeBringUpCompletes(t *testing.T) {
	ctx := context.Background()
	bmc := &gatedBMC{Fake: redfish.NewFake(), release: make(chan struct{})}
	orch, store := newAsyncOrch(t, bmc)

	done := make(chan models.Job, 1)
	go func() {
		j, err := orch.StartAsync(ctx, asyncReq())
		if err != nil {
			t.Errorf("StartAsync: %v", err)
		}
		done <- j
	}()

	var j models.Job
	select {
	case j = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("StartAsync blocked on BMC bring-up; it must return once the job row is durable")
	}
	if j.ID == "" {
		t.Fatal("StartAsync returned no job id")
	}

	// Bring-up must genuinely still be pending -- otherwise this test would
	// pass even if StartAsync had simply run everything synchronously.
	if bmc.didInsert() {
		t.Fatal("media insert completed before StartAsync returned; bring-up was not backgrounded")
	}
	// The job must already be observable, since polling Get is the only way a
	// caller learns what happens next.
	if got, err := store.Get(ctx, j.ID); err != nil || got.ID != j.ID {
		t.Fatalf("job not durable at return: got %+v err=%v", got, err)
	}

	close(bmc.release)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !bmc.didInsert() {
		time.Sleep(10 * time.Millisecond)
	}
	if !bmc.didInsert() {
		t.Fatal("background bring-up never inserted media after release")
	}
}

// TestStartAsyncBringUpFailureBecomesTerminalState proves failures are not lost
// by moving off the caller's goroutine: StartAsync reports success (the job was
// created), and the bring-up failure surfaces as a terminal job state on a
// later poll -- the only place an async caller can see it.
func TestStartAsyncBringUpFailureBecomesTerminalState(t *testing.T) {
	ctx := context.Background()
	bmc := &gatedBMC{Fake: redfish.NewFake(), insertErr: errors.New("boom: media insert refused")}
	orch, store := newAsyncOrch(t, bmc)

	j, err := orch.StartAsync(ctx, asyncReq())
	if err != nil {
		t.Fatalf("StartAsync should not surface bring-up failure: %v", err)
	}

	var final models.Job
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		final, err = store.Get(ctx, j.ID)
		if err != nil {
			t.Fatal(err)
		}
		if final.State == models.StateFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if final.State != models.StateFailed {
		t.Fatalf("want failed after bring-up error, got state=%s phase=%s", final.State, final.Phase)
	}
}

// TestStartAsyncSurvivesCallerCancellation pins the other half of the fix: a
// caller that gives up (an HTTP client hitting its timeout) must not abort a
// start that already committed to touching the BMC. This is the failure that
// produced "jobstore: insert: context canceled" against real hardware.
func TestStartAsyncSurvivesCallerCancellation(t *testing.T) {
	bmc := &gatedBMC{Fake: redfish.NewFake(), release: make(chan struct{})}
	orch, store := newAsyncOrch(t, bmc)

	// Mirrors the handler: cancellation detached, values kept.
	reqCtx, cancel := context.WithCancel(context.Background())
	j, err := orch.StartAsync(context.WithoutCancel(reqCtx), asyncReq())
	if err != nil {
		t.Fatalf("StartAsync: %v", err)
	}
	cancel() // client disconnects while bring-up is still in flight
	close(bmc.release)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !bmc.didInsert() {
		time.Sleep(10 * time.Millisecond)
	}
	if !bmc.didInsert() {
		t.Fatal("caller cancellation aborted bring-up; it must be detached")
	}
	if got, err := store.Get(context.Background(), j.ID); err != nil || got.ID == "" {
		t.Fatalf("job row lost after caller cancellation: %+v err=%v", got, err)
	}
}
