package sol

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/mattcburns/shoal/internal/common/jobport"
	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/watchport"
)

// DefaultStallTimeout is used when WatchSession.StallTimeout is zero.
const DefaultStallTimeout = 90 * time.Second

// WatchService implements watchport.WatchRegistrar using a SOL Transport factory.
type WatchService struct {
	log      *slog.Logger
	progress jobport.JobProgress
	// NewTransport builds a transport for a session (libvirt by default).
	NewTransport func(session models.WatchSession) Transport

	mu       sync.Mutex
	active   map[string]*activeWatch // sessionID -> watch
	byDevice map[string]string       // deviceID -> sessionID (single ownership)
}

type activeWatch struct {
	session models.WatchSession
	cancel  context.CancelFunc
	trans   Transport
	done    chan struct{}
}

// NewWatchService constructs a WatchService. progress must be non-nil before Register.
func NewWatchService(log *slog.Logger, progress jobport.JobProgress) *WatchService {
	if log == nil {
		log = slog.Default()
	}
	return &WatchService{
		log:      log,
		progress: progress,
		active:   make(map[string]*activeWatch),
		byDevice: make(map[string]string),
		NewTransport: func(session models.WatchSession) Transport {
			switch session.Transport {
			case "libvirt", "":
				return &LibvirtTransport{}
			default:
				// Unrecognized transport (including "redfish_sol" when the
				// composition root hasn't wired a real factory, or any
				// legacy/typo value) must fail loudly rather than silently
				// tailing a libvirt console that doesn't exist.
				return &errorTransport{err: fmt.Errorf("sol: unsupported serial transport %q", session.Transport)}
			}
		},
	}
}

// SetProgress injects the job progress port (composition root may wire late).
func (w *WatchService) SetProgress(p jobport.JobProgress) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.progress = p
}

// Register starts a SOL tail for the session. Rejects dual ownership of a device.
func (w *WatchService) Register(ctx context.Context, session models.WatchSession) error {
	if session.ID == "" || session.JobID == "" || session.DeviceID == "" {
		return fmt.Errorf("sol: watch session missing id/job/device")
	}
	if session.Target == "" {
		return fmt.Errorf("sol: watch session missing target")
	}
	if !session.StallDisabled && session.StallTimeout <= 0 {
		session.StallTimeout = DefaultStallTimeout
	}

	w.mu.Lock()
	if w.progress == nil {
		w.mu.Unlock()
		return fmt.Errorf("sol: progress port not configured")
	}
	if _, ok := w.byDevice[session.DeviceID]; ok {
		w.mu.Unlock()
		return fmt.Errorf("sol: device %s already has an active SOL watch", session.DeviceID)
	}
	if _, ok := w.active[session.ID]; ok {
		w.mu.Unlock()
		return fmt.Errorf("sol: session %s already registered", session.ID)
	}

	trans := w.NewTransport(session)
	watchCtx, cancel := context.WithCancel(context.Background())
	aw := &activeWatch{
		session: session,
		cancel:  cancel,
		trans:   trans,
		done:    make(chan struct{}),
	}
	w.active[session.ID] = aw
	w.byDevice[session.DeviceID] = session.ID
	progress := w.progress
	w.mu.Unlock()

	lines, err := trans.Open(watchCtx, session.Target)
	if err != nil {
		w.cleanupLocked(session)
		return fmt.Errorf("sol: open transport: %w", err)
	}

	go w.run(watchCtx, aw, lines, progress)
	w.log.Info("sol watch registered",
		"session_id", session.ID,
		"job_id", session.JobID,
		"device_id", session.DeviceID,
		"target", session.Target,
	)
	return nil
}

func (w *WatchService) cleanupLocked(session models.WatchSession) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if aw, ok := w.active[session.ID]; ok {
		aw.cancel()
		_ = aw.trans.Close()
		delete(w.active, session.ID)
	}
	delete(w.byDevice, session.DeviceID)
}

// Unregister stops the watch for sessionID.
// Close is bounded so Deploy HandleTerminal cannot hang on a stuck SSH/PTY.
func (w *WatchService) Unregister(_ context.Context, sessionID string) error {
	w.mu.Lock()
	aw, ok := w.active[sessionID]
	if !ok {
		w.mu.Unlock()
		return nil
	}
	delete(w.active, sessionID)
	delete(w.byDevice, aw.session.DeviceID)
	w.mu.Unlock()

	aw.cancel()

	closeDone := make(chan struct{})
	go func() {
		_ = aw.trans.Close()
		close(closeDone)
	}()
	select {
	case <-closeDone:
	case <-time.After(5 * time.Second):
		w.log.Warn("sol transport close timed out", "session_id", sessionID)
	}

	select {
	case <-aw.done:
	case <-time.After(2 * time.Second):
		w.log.Warn("sol watch run did not exit promptly", "session_id", sessionID)
	}
	w.log.Info("sol watch unregistered", "session_id", sessionID)
	return nil
}

func (w *WatchService) run(ctx context.Context, aw *activeWatch, lines <-chan string, progress jobport.JobProgress) {
	defer close(aw.done)
	stallDisabled := aw.session.StallDisabled
	stall := aw.session.StallTimeout
	var timer *time.Timer
	var timerC <-chan time.Time
	if !stallDisabled {
		timer = time.NewTimer(stall)
		timerC = timer.C
		defer timer.Stop()
	}

	resetStall := func() {
		if stallDisabled || timer == nil {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(stall)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-timerC:
			w.log.Warn("sol stall detected", "job_id", aw.session.JobID, "timeout", stall.String())
			_ = progress.ReportStall(context.Background(), aw.session.JobID, fmt.Sprintf("no SOL marker for %s", stall))
			return
		case line, ok := <-lines:
			if !ok {
				// Stream ended without a terminal marker (or after watch cancel).
				// Deploy ignores transport errors once the job is no longer PROVISIONING.
				_ = progress.ReportTransportError(context.Background(), aw.session.JobID, fmt.Errorf("sol stream closed"))
				return
			}
			m, ok := ParseLine(line)
			if !ok {
				continue
			}
			resetStall()
			if err := progress.ApplyMarker(context.Background(), aw.session.JobID, m); err != nil {
				w.log.Error("apply marker failed", "job_id", aw.session.JobID, "err", err.Error())
			}
			// Stage-complete or job-terminal markers: stop the watch loop so
			// guest poweroff / Unregister does not race as a transport error.
			// PREP_DONE is stage-complete only (job continues on next media).
			if IsStageComplete(m) {
				return
			}
		}
	}
}

// ActiveCount returns the number of active watches (test helper).
func (w *WatchService) ActiveCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.active)
}

// HasWatch reports whether deviceID currently has an active SOL session.
func (w *WatchService) HasWatch(deviceID string) bool {
	if deviceID == "" {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.byDevice[deviceID]
	return ok
}

// WatchingDeviceIDs returns device IDs with an active SOL watch.
func (w *WatchService) WatchingDeviceIDs() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, 0, len(w.byDevice))
	for id := range w.byDevice {
		out = append(out, id)
	}
	return out
}

var _ watchport.WatchRegistrar = (*WatchService)(nil)
