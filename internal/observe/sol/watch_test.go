package sol_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/observe/sol"
)

type recordingProgress struct {
	mu      sync.Mutex
	markers []models.SOLMarker
	stalls  int
}

func (r *recordingProgress) ApplyMarker(_ context.Context, _ string, m models.SOLMarker) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.markers = append(r.markers, m)
	return nil
}
func (r *recordingProgress) ReportStall(context.Context, string, string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stalls++
	return nil
}
func (r *recordingProgress) ReportTransportError(context.Context, string, error) error { return nil }

func TestWatchServiceMarkers(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	prog := &recordingProgress{}
	w := sol.NewWatchService(log, prog)
	pr, pw := io.Pipe()
	w.NewTransport = func(models.WatchSession) sol.Transport {
		return sol.NewReaderTransport(pr)
	}
	err := w.Register(context.Background(), models.WatchSession{
		ID: "s1", JobID: "j1", DeviceID: "d1", Target: "x",
		StallTimeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	// dual ownership rejected
	if err := w.Register(context.Background(), models.WatchSession{
		ID: "s2", JobID: "j2", DeviceID: "d1", Target: "y", StallTimeout: time.Minute,
	}); err == nil {
		t.Fatal("expected dual ownership error")
	}

	_, _ = io.WriteString(pw, "noise\n")
	_, _ = io.WriteString(pw, "SHOAL|1|1|2026-06-19T04:10:00Z|BOOT|1|OK|hi\n")
	time.Sleep(50 * time.Millisecond)
	prog.mu.Lock()
	n := len(prog.markers)
	prog.mu.Unlock()
	if n != 1 {
		t.Fatalf("markers=%d", n)
	}
	_ = w.Unregister(context.Background(), "s1")
	_ = pw.Close()
}

func TestWatchServiceStallDisabled(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	prog := &recordingProgress{}
	w := sol.NewWatchService(log, prog)
	pr, pw := io.Pipe()
	w.NewTransport = func(models.WatchSession) sol.Transport {
		return sol.NewReaderTransport(pr)
	}
	err := w.Register(context.Background(), models.WatchSession{
		ID: "s1", JobID: "j1", DeviceID: "d1", Target: "x",
		StallTimeout:  30 * time.Millisecond,
		StallDisabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	prog.mu.Lock()
	stalls := prog.stalls
	prog.mu.Unlock()
	if stalls != 0 {
		t.Fatalf("stalls=%d want 0 when StallDisabled", stalls)
	}
	_ = w.Unregister(context.Background(), "s1")
	_ = pw.Close()
}

func TestWatchServiceStopsOnTerminalMarker(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	prog := &recordingProgress{}
	w := sol.NewWatchService(log, prog)
	pr, pw := io.Pipe()
	w.NewTransport = func(models.WatchSession) sol.Transport {
		return sol.NewReaderTransport(pr)
	}
	ctx := context.Background()
	if err := w.Register(ctx, models.WatchSession{
		ID: "s1", JobID: "j1", DeviceID: "d1", Target: "t",
		StallTimeout: time.Minute,
	}); err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(pw, "SHOAL|1|1|2026-06-19T04:10:00Z|DONE|100|OK|done\n")
	// Give run loop time to apply terminal marker without closing the pipe.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		prog.mu.Lock()
		n := len(prog.markers)
		prog.mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	prog.mu.Lock()
	n := len(prog.markers)
	prog.mu.Unlock()
	if n < 1 {
		t.Fatal("expected terminal marker applied")
	}
	// Unregister should complete quickly even if pipe still open.
	done := make(chan error, 1)
	go func() { done <- w.Unregister(ctx, "s1") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Unregister hung after terminal marker")
	}
	_ = pw.Close()
}

func TestWatchServiceStall(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	prog := &recordingProgress{}
	w := sol.NewWatchService(log, prog)
	pr, pw := io.Pipe()
	defer pw.Close()
	w.NewTransport = func(models.WatchSession) sol.Transport {
		return sol.NewReaderTransport(pr)
	}
	err := w.Register(context.Background(), models.WatchSession{
		ID: "s1", JobID: "j1", DeviceID: "d1", Target: "x",
		StallTimeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		prog.mu.Lock()
		st := prog.stalls
		prog.mu.Unlock()
		if st > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("stall not reported")
}

// TestWatchServiceActivityWithoutMarkersPreventsStall proves the fix for a
// live-hardware finding: some BIOS/Lifecycle-Controller screens (Dell's
// Unified Server Configurator, confirmed via a raw SOL capture) repaint via
// cursor-positioning escapes with no newline for minutes at a time. Before
// this fix, the stall timer only reset on parsed SHOAL| markers, so a job
// could report "stall" while the console was actively printing -- just not
// anything that looked like a complete line, let alone a marker.
func TestWatchServiceActivityWithoutMarkersPreventsStall(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	prog := &recordingProgress{}
	w := sol.NewWatchService(log, prog)
	pr, pw := io.Pipe()
	w.NewTransport = func(models.WatchSession) sol.Transport {
		return sol.NewReaderTransport(pr)
	}
	err := w.Register(context.Background(), models.WatchSession{
		ID: "s1", JobID: "j1", DeviceID: "d1", Target: "x",
		StallTimeout: 60 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	// No newline, no SHOAL| marker -- just raw bytes, faster than the stall
	// timeout, for several timeout-multiples.
	stop := make(chan struct{})
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = io.WriteString(pw, "\x1b[12;34Hnoise-no-newline")
			time.Sleep(15 * time.Millisecond)
		}
	}()

	time.Sleep(300 * time.Millisecond) // ~5x stall timeout of continuous noise
	close(stop)
	<-writeDone

	prog.mu.Lock()
	stalledDuringNoise := prog.stalls
	prog.mu.Unlock()
	if stalledDuringNoise != 0 {
		t.Fatalf("stalled during continuous non-marker activity: stalls=%d", stalledDuringNoise)
	}

	// Now go silent: a real stall must still fire (this isn't "never stall").
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		prog.mu.Lock()
		st := prog.stalls
		prog.mu.Unlock()
		if st > 0 {
			_ = pw.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = pw.Close()
	t.Fatal("stall not reported after activity stopped")
}

// TestWatchServiceDefaultTransportRejectsUnknown proves the default
// NewTransport factory (used when a caller never wires a combinator) fails
// loudly for an unrecognized session.Transport instead of silently tailing a
// libvirt console that doesn't exist.
func TestWatchServiceDefaultTransportRejectsUnknown(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	prog := &recordingProgress{}
	w := sol.NewWatchService(log, prog) // default NewTransport, not overridden

	err := w.Register(context.Background(), models.WatchSession{
		ID: "s1", JobID: "j1", DeviceID: "d1",
		Transport: "redfish_sol", Target: "x",
		StallTimeout: time.Minute,
	})
	if err == nil {
		t.Fatal("expected error for unwired redfish_sol transport, got nil")
	}
	if w.ActiveCount() != 0 {
		t.Fatalf("expected no active watch after failed Open, got %d", w.ActiveCount())
	}

	// A legacy/typo value must also fail, never silently fall back to libvirt.
	err = w.Register(context.Background(), models.WatchSession{
		ID: "s2", JobID: "j2", DeviceID: "d2",
		Transport: "ipmi_sol", Target: "x",
		StallTimeout: time.Minute,
	})
	if err == nil {
		t.Fatal("expected error for ipmi_sol transport, got nil")
	}
}
