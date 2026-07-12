// Package job implements the Deploy Orchestrator, HandleTerminal, and jobport adapter.
// Orchestrator is the sole lifecycle writer.
package job

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/mattcburns/shoal/internal/common/jobport"
	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/common/secrets"
	"github.com/mattcburns/shoal/internal/common/validate"
	"github.com/mattcburns/shoal/internal/common/watchport"
	"github.com/mattcburns/shoal/internal/deploy/jobstore"
	"github.com/mattcburns/shoal/internal/observe/sol"
)

// TerminalReason classifies why a job is ending.
type TerminalReason string

const (
	ReasonDoneOK      TerminalReason = "done_ok"
	ReasonMarkerError TerminalReason = "marker_error"
	ReasonStall       TerminalReason = "stall"
	ReasonTransport   TerminalReason = "transport"
	ReasonCancel      TerminalReason = "cancel"
	ReasonBMC         TerminalReason = "bmc"
	ReasonPanic       TerminalReason = "panic"
)

// Orchestrator owns lifecycle transitions, BMC actions, and cleanup.
type Orchestrator struct {
	log        *slog.Logger
	store      jobstore.Store
	secrets    secrets.Backend
	newBMC     redfish.Factory
	watches    watchport.WatchRegistrar
	authMode   string
	tlsMode    string
	caFile     string
	failOrphan bool

	mu       sync.Mutex
	running  map[string]*runState // jobID -> state
	terminal chan terminalEvent
	// closed when Stop is called
	stopCh chan struct{}
	wg     sync.WaitGroup
}

type runState struct {
	cancel     context.CancelFunc
	systemID   string
	sessionID  string
	credential string
	bmcURL     string
	// terminalOnce ensures HandleTerminal runs once
	terminalOnce sync.Once
}

type terminalEvent struct {
	jobID  string
	reason TerminalReason
}

// Options configures the Orchestrator.
type Options struct {
	Log                 *slog.Logger
	Store               jobstore.Store
	Secrets             secrets.Backend
	NewBMC              redfish.Factory
	Watches             watchport.WatchRegistrar
	AuthMode            string
	TLSMode             string
	CAFile              string
	ReconcileFailOrphan bool
}

// NewOrchestrator constructs an Orchestrator and starts the terminal worker.
func NewOrchestrator(opts Options) *Orchestrator {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	if opts.NewBMC == nil {
		opts.NewBMC = redfish.NewBMC
	}
	auth := opts.AuthMode
	if auth == "" {
		auth = "basic"
	}
	tlsMode := opts.TLSMode
	if tlsMode == "" {
		tlsMode = "off"
	}
	o := &Orchestrator{
		log:        log,
		store:      opts.Store,
		secrets:    opts.Secrets,
		newBMC:     opts.NewBMC,
		watches:    opts.Watches,
		authMode:   auth,
		tlsMode:    tlsMode,
		caFile:     opts.CAFile,
		failOrphan: opts.ReconcileFailOrphan, // composition root passes config default true
		running:    make(map[string]*runState),
		terminal:   make(chan terminalEvent, 64),
		stopCh:     make(chan struct{}),
	}
	o.wg.Add(1)
	go o.terminalLoop()
	return o
}

// Stop stops the terminal worker.
func (o *Orchestrator) Stop() {
	select {
	case <-o.stopCh:
	default:
		close(o.stopCh)
	}
	o.wg.Wait()
}

func (o *Orchestrator) terminalLoop() {
	defer o.wg.Done()
	for {
		select {
		case <-o.stopCh:
			return
		case ev := <-o.terminal:
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			if err := o.HandleTerminal(ctx, ev.jobID, ev.reason); err != nil {
				o.log.Error("HandleTerminal failed", "job_id", ev.jobID, "reason", string(ev.reason), "err", err.Error())
			}
			cancel()
		}
	}
}

// ProgressPort returns the jobport adapter for Observe injection.
func (o *Orchestrator) ProgressPort() jobport.JobProgress {
	return &progressAdapter{orch: o}
}

// SetWatchRegistrar injects the watch port (composition root).
func (o *Orchestrator) SetWatchRegistrar(w watchport.WatchRegistrar) {
	o.watches = w
}

// Get returns a job from the store.
func (o *Orchestrator) Get(ctx context.Context, jobID string) (models.ProvisioningJob, error) {
	return o.store.Get(ctx, jobID)
}

// Start begins a provisioning job using CLI/API binding fields (no NetBox).
func (o *Orchestrator) Start(ctx context.Context, req models.StartJobRequest) (models.ProvisioningJob, error) {
	if err := validate.StartJobRequest(req); err != nil {
		return models.ProvisioningJob{}, err
	}
	if o.watches == nil {
		return models.ProvisioningJob{}, fmt.Errorf("job: watch registrar not configured")
	}

	jobID := newID()
	credRef := req.CredentialRef
	if credRef == "" {
		credRef = "job-" + jobID
		if err := o.secrets.Put(ctx, credRef, secrets.Credential{
			Username: req.BMCUsername,
			Password: req.BMCPassword,
		}); err != nil {
			return models.ProvisioningJob{}, fmt.Errorf("job: store credentials: %w", err)
		}
	}
	cred, err := o.secrets.Get(ctx, credRef)
	if err != nil {
		return models.ProvisioningJob{}, fmt.Errorf("job: resolve credentials: %w", err)
	}

	profile := req.ProfileRef
	if profile == "" {
		profile = "spike"
	}
	now := time.Now().UTC()
	job := models.ProvisioningJob{
		ID:          jobID,
		DeviceID:    req.DeviceID,
		ProfileRef:  profile,
		State:       models.StateProvisioning,
		Attempt:     1,
		Phase:       "STARTING",
		StartedAt:   &now,
		UpdatedAt:   &now,
		ISOURL:      req.ISOURL,
		BMCEndpoint: req.BMCEndpoint,
	}
	if err := o.store.Insert(ctx, job); err != nil {
		return models.ProvisioningJob{}, err
	}

	jobCtx, cancel := context.WithCancel(context.Background())
	rs := &runState{
		cancel:     cancel,
		systemID:   req.SystemID,
		credential: credRef,
		bmcURL:     req.BMCEndpoint,
		sessionID:  "sol-" + jobID,
	}
	o.mu.Lock()
	o.running[jobID] = rs
	o.mu.Unlock()

	if err := o.provision(jobCtx, job, req, cred, rs); err != nil {
		o.log.Error("provision start failed", "job_id", jobID, "err", err.Error())
		_ = o.HandleTerminal(ctx, jobID, ReasonBMC)
		j, _ := o.store.Get(ctx, jobID)
		if j.ID == "" {
			return job, err
		}
		return j, err
	}

	out, err := o.store.Get(ctx, jobID)
	if err != nil {
		return job, nil
	}
	return out, nil
}

func (o *Orchestrator) provision(ctx context.Context, job models.ProvisioningJob, req models.StartJobRequest, cred secrets.Credential, rs *runState) error {
	bmc, err := o.newBMC(redfish.Config{
		BaseURL:       req.BMCEndpoint,
		Username:      cred.Username,
		Password:      cred.Password,
		AuthMode:      o.authMode,
		TLSMode:       o.tlsMode,
		CAFile:        o.caFile,
		MaxConcurrent: 1,
	})
	if err != nil {
		return err
	}
	if err := bmc.Open(ctx); err != nil {
		return fmt.Errorf("bmc open: %w", err)
	}
	defer func() { _ = bmc.Close(context.Background()) }()

	// Prefer explicit SystemID; else match Redfish Name to DeviceID (lab: shoal-node-1).
	lookup := req.SystemID
	if lookup == "" {
		lookup = req.DeviceID
	}
	sys, err := bmc.GetSystem(ctx, lookup)
	if err != nil {
		// Last resort: if DeviceID lookup failed and SystemID was empty, try empty only when single system.
		if req.SystemID == "" && req.DeviceID != "" {
			sys, err = bmc.GetSystem(ctx, "")
		}
		if err != nil {
			return fmt.Errorf("get system: %w", err)
		}
	}
	rs.systemID = sys.ID

	vms, err := bmc.ListVirtualMedia(ctx, sys.ID)
	if err != nil {
		return fmt.Errorf("list virtual media: %w", err)
	}
	mediaURI := pickCDMedia(vms)
	if mediaURI == "" {
		return fmt.Errorf("no CD-capable virtual media slot")
	}
	if err := bmc.InsertVirtualMedia(ctx, mediaURI, req.ISOURL); err != nil {
		return fmt.Errorf("insert media: %w", err)
	}
	if err := bmc.SetBootOverrideOnceCD(ctx, sys.ID); err != nil {
		return fmt.Errorf("boot override: %w", err)
	}
	if err := bmc.Power(ctx, sys.ID, "On"); err != nil {
		return fmt.Errorf("power on: %w", err)
	}

	_ = o.store.UpdateProgress(ctx, job.ID, "WAITING_SOL", nil, 0, "")

	stall := req.StallTimeout
	if stall <= 0 {
		// Live ISO boot can take well over 90s before the first marker.
		stall = 3 * time.Minute
	}
	session := models.WatchSession{
		ID:           rs.sessionID,
		JobID:        job.ID,
		DeviceID:     req.DeviceID,
		Transport:    "libvirt",
		Target:       req.SerialTarget,
		StartedAt:    time.Now().UTC(),
		StallTimeout: stall,
	}
	// Persist sol session id via progress soft field isn't ideal; update job through transition is wrong.
	// Use UpdateProgress for phase only; SOLSessionID stored in runState + optional store field via re-insert not available.
	// Patch: store Transition doesn't set sol_session_id. Memory/Postgres need a SetSession helper or we encode in running map only.
	// For status API, re-read job won't show session — acceptable for Phase 2 if we UpdateProgress detail.
	_ = o.store.UpdateProgress(ctx, job.ID, "WAITING_SOL", nil, 0, "")

	if err := o.watches.Register(ctx, session); err != nil {
		_ = bmc.CleanupMediaAndBoot(ctx, sys.ID)
		return fmt.Errorf("register watch: %w", err)
	}
	o.log.Info("job started",
		"job_id", job.ID,
		"device_id", job.DeviceID,
		"bmc", req.BMCEndpoint,
		"iso_url", req.ISOURL,
		// never log password
	)
	return nil
}

func pickCDMedia(vms []redfish.VirtualMedia) string {
	for _, vm := range vms {
		if vm.SupportsCD {
			return vm.URI
		}
	}
	if len(vms) > 0 {
		return vms[0].URI
	}
	return ""
}

// Cancel requests terminal handling for a job.
func (o *Orchestrator) Cancel(ctx context.Context, jobID string) error {
	job, err := o.store.Get(ctx, jobID)
	if err != nil {
		return err // includes jobstore.ErrNotFound
	}
	if job.State != models.StateProvisioning {
		return fmt.Errorf("job: cannot cancel job in state %s", job.State)
	}
	o.enqueueTerminal(jobID, ReasonCancel)
	return nil
}

// HandleTerminal runs cleanup and commits the terminal lifecycle state.
// Safe to call multiple times; only the first call performs work.
func (o *Orchestrator) HandleTerminal(ctx context.Context, jobID string, reason TerminalReason) error {
	o.mu.Lock()
	rs, ok := o.running[jobID]
	o.mu.Unlock()

	runOnce := func(fn func()) {
		if ok && rs != nil {
			rs.terminalOnce.Do(fn)
			return
		}
		// orphan path without runState
		fn()
	}

	var handleErr error
	runOnce(func() {
		handleErr = o.handleTerminalOnce(ctx, jobID, reason, rs)
	})
	return handleErr
}

func (o *Orchestrator) handleTerminalOnce(ctx context.Context, jobID string, reason TerminalReason, rs *runState) error {
	o.log.Info("HandleTerminal", "job_id", jobID, "reason", string(reason))

	if rs != nil && rs.cancel != nil {
		rs.cancel()
	}

	// Unregister watch first so SOL stops feeding markers.
	// Bound the wait: a stuck SSH/PTY close must not prevent lifecycle transition.
	sessionID := ""
	systemID := ""
	bmcURL := ""
	credRef := ""
	if rs != nil {
		sessionID = rs.sessionID
		systemID = rs.systemID
		bmcURL = rs.bmcURL
		credRef = rs.credential
	}
	if sessionID != "" && o.watches != nil {
		unregDone := make(chan struct{})
		go func() {
			_ = o.watches.Unregister(context.Background(), sessionID)
			close(unregDone)
		}()
		select {
		case <-unregDone:
		case <-time.After(10 * time.Second):
			o.log.Warn("watch unregister timed out", "job_id", jobID, "session_id", sessionID)
		case <-ctx.Done():
			o.log.Warn("watch unregister aborted by context", "job_id", jobID)
		}
	}

	// Load job for BMC endpoint if needed. Prefer Background so a cancelled
	// caller context cannot skip the terminal state write.
	job, err := o.store.Get(context.Background(), jobID)
	if err != nil {
		return err
	}
	if bmcURL == "" {
		bmcURL = job.BMCEndpoint
	}

	// Always-run cleanup when we have BMC coordinates (hard timeout so we still transition).
	if bmcURL != "" {
		cctx, ccancel := context.WithTimeout(context.Background(), 45*time.Second)
		err := o.cleanupBMC(cctx, bmcURL, credRef, systemID)
		ccancel()
		if err != nil {
			o.log.Error("bmc cleanup failed", "job_id", jobID, "err", err.Error())
			// still transition
		}
	}

	var to models.LifecycleState
	var errMsg string
	switch reason {
	case ReasonDoneOK:
		to = models.StateProvisioned
		errMsg = ""
	case ReasonCancel:
		to = models.StateFailed
		errMsg = "canceled"
	case ReasonStall:
		to = models.StateFailed
		errMsg = "sol stall"
	case ReasonTransport:
		to = models.StateFailed
		errMsg = "sol transport error"
	case ReasonMarkerError:
		to = models.StateFailed
		errMsg = "marker error"
		if job.Error != "" {
			errMsg = job.Error
		}
	case ReasonBMC:
		to = models.StateFailed
		errMsg = "bmc error"
		if job.Error != "" {
			errMsg = job.Error
		}
	default:
		to = models.StateFailed
		errMsg = string(reason)
	}

	// Always commit lifecycle with Background so waiters observe the terminal state
	// even if the terminalLoop request context already expired.
	if err := o.store.Transition(context.Background(), jobID, to, errMsg); err != nil {
		return err
	}
	o.log.Info("job terminal", "job_id", jobID, "state", string(to), "reason", string(reason))

	o.mu.Lock()
	delete(o.running, jobID)
	o.mu.Unlock()
	return nil
}

func (o *Orchestrator) cleanupBMC(ctx context.Context, bmcURL, credRef, systemID string) error {
	user, pass := "", ""
	if credRef != "" && o.secrets != nil {
		if c, err := o.secrets.Get(ctx, credRef); err == nil {
			user, pass = c.Username, c.Password
		}
	}
	done := make(chan error, 1)
	go func() {
		bmc, err := o.newBMC(redfish.Config{
			BaseURL:        bmcURL,
			Username:       user,
			Password:       pass,
			AuthMode:       o.authMode,
			TLSMode:        o.tlsMode,
			CAFile:         o.caFile,
			RequestTimeout: 20 * time.Second,
			MaxConcurrent:  1,
		})
		if err != nil {
			done <- err
			return
		}
		if err := bmc.Open(ctx); err != nil {
			done <- err
			return
		}
		cerr := bmc.CleanupMediaAndBoot(ctx, systemID)
		_ = bmc.Close(context.Background())
		done <- cerr
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("bmc cleanup timed out: %w", ctx.Err())
	}
}

// ReconcileOrphans fails in-flight PROVISIONING jobs left from a previous process.
// Default policy (failOrphan=true): cleanup + FAILED for each.
func (o *Orchestrator) ReconcileOrphans(ctx context.Context) error {
	jobs, err := o.store.ListByState(ctx, models.StateProvisioning)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		o.log.Warn("reconciling orphan job", "job_id", j.ID, "device_id", j.DeviceID, "fail", o.failOrphan)
		if !o.failOrphan {
			continue
		}
		// Seed runState for cleanup coordinates.
		o.mu.Lock()
		if _, ok := o.running[j.ID]; !ok {
			o.running[j.ID] = &runState{
				bmcURL:     j.BMCEndpoint,
				sessionID:  j.SOLSessionID,
				systemID:   "",
				credential: "", // may fail cleanup without creds; still transition
			}
		}
		o.mu.Unlock()
		if err := o.HandleTerminal(ctx, j.ID, ReasonBMC); err != nil {
			o.log.Error("orphan reconcile failed", "job_id", j.ID, "err", err.Error())
		}
	}
	return nil
}

func (o *Orchestrator) enqueueTerminal(jobID string, reason TerminalReason) {
	select {
	case o.terminal <- terminalEvent{jobID: jobID, reason: reason}:
	default:
		// channel full — run async
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			_ = o.HandleTerminal(ctx, jobID, reason)
		}()
	}
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// progressAdapter implements jobport.JobProgress.
type progressAdapter struct {
	orch *Orchestrator
}

func (p *progressAdapter) ApplyMarker(ctx context.Context, jobID string, m models.SOLMarker) error {
	soft := ""
	if m.State == "ERROR" || m.State == "WARN" {
		soft = m.Detail
		if soft == "" {
			soft = m.State
		}
	}
	phase := m.Phase
	if m.State == "HEARTBEAT" && phase == "" {
		phase = "HEARTBEAT"
	}
	if err := p.orch.store.UpdateProgress(ctx, jobID, phase, m.Percent, m.Seq, soft); err != nil {
		return err
	}
	if sol.IsTerminal(m) {
		reason := TerminalReason(sol.TerminalReasonFromMarker(m))
		p.orch.enqueueTerminal(jobID, reason)
	}
	return nil
}

func (p *progressAdapter) ReportStall(ctx context.Context, jobID string, reason string) error {
	if !p.stillProvisioning(ctx, jobID) {
		return nil
	}
	_ = p.orch.store.UpdateProgress(ctx, jobID, "STALL", nil, 0, reason)
	p.orch.enqueueTerminal(jobID, ReasonStall)
	return nil
}

func (p *progressAdapter) ReportTransportError(ctx context.Context, jobID string, err error) error {
	// Ignore stream close after DONE/cancel/stall already committed (e.g. guest poweroff).
	if !p.stillProvisioning(ctx, jobID) {
		return nil
	}
	msg := "transport error"
	if err != nil {
		msg = err.Error()
	}
	_ = p.orch.store.UpdateProgress(ctx, jobID, "TRANSPORT", nil, 0, msg)
	p.orch.enqueueTerminal(jobID, ReasonTransport)
	return nil
}

func (p *progressAdapter) stillProvisioning(ctx context.Context, jobID string) bool {
	j, err := p.orch.store.Get(ctx, jobID)
	if err != nil {
		return true // fail open so real errors still surface
	}
	return j.State == models.StateProvisioning
}

var _ jobport.JobProgress = (*progressAdapter)(nil)
