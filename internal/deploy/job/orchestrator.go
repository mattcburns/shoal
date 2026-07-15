// Package job implements the Deploy Orchestrator, HandleTerminal, and jobport adapter.
// Orchestrator is the sole lifecycle writer.
package job

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/mattcburns/shoal/internal/common/jobport"
	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/netbox"
	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/common/secrets"
	"github.com/mattcburns/shoal/internal/common/validate"
	"github.com/mattcburns/shoal/internal/common/watchport"
	"github.com/mattcburns/shoal/internal/core/profile"
	"github.com/mattcburns/shoal/internal/deploy/iso"
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

// Reliability timeouts (Phase 5 polish — named constants).
const (
	CleanupTimeout        = 45 * time.Second
	UnregisterTimeout     = 10 * time.Second
	TerminalWorkerTimeout = 2 * time.Minute
	DefaultSOLStall       = 3 * time.Minute
)

// Orchestrator owns lifecycle transitions, BMC actions, and cleanup.
type Orchestrator struct {
	log           *slog.Logger
	store         jobstore.Store
	secrets       secrets.Backend
	newBMC        redfish.Factory
	watches       watchport.WatchRegistrar
	netbox        netbox.LifecycleWriter // optional; identity lifecycle only
	profiles      profile.Store          // optional; Phase 5b approval gate
	isoBaseURL    string                 // Phase 5c: resolve profile iso_base → URL
	isoBuilder    iso.Builder            // optional Phase 6a dynamic build
	isoPublishDir string
	isoDynamic    bool // SHOAL_ISO_DYNAMIC: build when ISOURL empty + publish configured
	authMode      string
	tlsMode       string
	caFile        string
	failOrphan    bool

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
	// terminalQueued is set under Orchestrator.mu so only the first terminal
	// reason (cancel, DONE, stall, transport, …) is accepted. Without this,
	// Unregister-driven stream close races cancel and can win with "sol transport error".
	terminalQueued bool
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
	NetBox              netbox.LifecycleWriter // optional Phase 5 lifecycle sync
	Profiles            profile.Store          // optional Phase 5b profile load/approval
	ISOBaseURL          string                 // optional Phase 5c profile → ISO URL resolve
	ISOBuilder          iso.Builder            // optional Phase 6a dynamic build
	ISOPublishDir       string                 // publish dir for dynamic build
	ISODynamic          bool                   // build when ISOURL empty (needs builder+publish+base)
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
		log:           log,
		store:         opts.Store,
		secrets:       opts.Secrets,
		newBMC:        opts.NewBMC,
		watches:       opts.Watches,
		netbox:        opts.NetBox,
		profiles:      opts.Profiles,
		isoBaseURL:    opts.ISOBaseURL,
		isoBuilder:    opts.ISOBuilder,
		isoPublishDir: opts.ISOPublishDir,
		isoDynamic:    opts.ISODynamic,
		authMode:      auth,
		tlsMode:       tlsMode,
		caFile:        opts.CAFile,
		failOrphan:    opts.ReconcileFailOrphan, // composition root passes config default true
		running:       make(map[string]*runState),
		terminal:      make(chan terminalEvent, 64),
		stopCh:        make(chan struct{}),
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
			ctx, cancel := context.WithTimeout(context.Background(), TerminalWorkerTimeout)
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

// Start begins a provisioning job using CLI/API binding fields.
// When NetBox is configured, best-effort lifecycle_state=provisioning is written
// (failure is logged and does not block BMC actions).
// Profiles with NeedsApproval/DestructSteps require store approval or ApproveDestruct.
// When ISOURL is empty and a non-spike profile is loaded, iso_base is resolved via
// SHOAL_ISO_BASE_URL (Phase 5c). Optional BuildISO / SHOAL_ISO_DYNAMIC builds and
// publishes a live image first (Phase 6a).
func (o *Orchestrator) Start(ctx context.Context, req models.StartJobRequest) (models.ProvisioningJob, error) {
	if err := validate.StartJobRequest(req); err != nil {
		return models.ProvisioningJob{}, err
	}
	if o.watches == nil {
		return models.ProvisioningJob{}, fmt.Errorf("job: watch registrar not configured")
	}

	profileRef := req.ProfileRef
	if profileRef == "" {
		profileRef = "spike"
	}
	if err := o.checkProfileApproval(ctx, profileRef, req.ApproveDestruct); err != nil {
		return models.ProvisioningJob{}, err
	}
	if err := o.maybeBuildISO(ctx, &req, profileRef); err != nil {
		return models.ProvisioningJob{}, err
	}
	if err := o.resolveISOURL(ctx, &req, profileRef); err != nil {
		return models.ProvisioningJob{}, err
	}
	if req.ISOURL == "" {
		return models.ProvisioningJob{}, fmt.Errorf("job: iso_url is required")
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

	profile := profileRef
	now := time.Now().UTC()
	sessionID := "sol-" + jobID
	job := models.ProvisioningJob{
		ID:            jobID,
		DeviceID:      req.DeviceID,
		ProfileRef:    profile,
		State:         models.StateProvisioning,
		Attempt:       1,
		Phase:         "STARTING",
		StartedAt:     &now,
		UpdatedAt:     &now,
		ISOURL:        req.ISOURL,
		BMCEndpoint:   req.BMCEndpoint,
		SystemID:      req.SystemID,
		CredentialRef: credRef,
		SOLSessionID:  sessionID,
	}
	if err := o.store.Insert(ctx, job); err != nil {
		return models.ProvisioningJob{}, err
	}
	o.syncNetBoxLifecycle(ctx, req.DeviceID, models.StateProvisioning)

	jobCtx, cancel := context.WithCancel(context.Background())
	rs := &runState{
		cancel:     cancel,
		systemID:   req.SystemID,
		credential: credRef,
		bmcURL:     req.BMCEndpoint,
		sessionID:  sessionID,
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
	// Persist runtime coords so a different process can cancel/orphan-cleanup.
	if err := o.store.UpdateRuntime(ctx, job.ID, sys.ID, rs.sessionID, rs.credential); err != nil {
		return fmt.Errorf("persist runtime: %w", err)
	}

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
		stall = DefaultSOLStall
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
		case <-time.After(UnregisterTimeout):
			o.log.Warn("watch unregister timed out", "job_id", jobID, "session_id", sessionID)
		case <-ctx.Done():
			o.log.Warn("watch unregister aborted by context", "job_id", jobID)
		}
	}

	// Load job for BMC coordinates. Prefer Background so a cancelled
	// caller context cannot skip the terminal state write.
	job, err := o.store.Get(context.Background(), jobID)
	if err != nil {
		return err
	}
	// Prefer in-memory runState; fall back to durable job fields (out-of-process cancel/orphan).
	if bmcURL == "" {
		bmcURL = job.BMCEndpoint
	}
	if systemID == "" {
		systemID = job.SystemID
	}
	if credRef == "" {
		credRef = job.CredentialRef
	}
	if sessionID == "" {
		sessionID = job.SOLSessionID
	}

	// Always-run cleanup when we have BMC coordinates (hard timeout so we still transition).
	var cleanupErr error
	if bmcURL != "" {
		cctx, ccancel := context.WithTimeout(context.Background(), CleanupTimeout)
		cleanupErr = o.cleanupBMC(cctx, bmcURL, credRef, systemID)
		ccancel()
		if cleanupErr != nil {
			o.log.Error("bmc cleanup failed", "job_id", jobID, "err", cleanupErr.Error())
		}
	}

	var to models.LifecycleState
	var errMsg string
	switch reason {
	case ReasonDoneOK:
		to = models.StateProvisioned
		errMsg = ""
		// Phase 5: DONE post-check — cleanup must have left media/boot clear.
		if cleanupErr != nil {
			to = models.StateFailed
			errMsg = "post-check: bmc cleanup incomplete: " + cleanupErr.Error()
			o.log.Warn("DONE post-check failed", "job_id", jobID, "err", cleanupErr.Error())
		} else if bmcURL != "" {
			if err := o.postCheckClean(context.Background(), bmcURL, credRef, systemID); err != nil {
				to = models.StateFailed
				errMsg = "post-check: " + err.Error()
				o.log.Warn("DONE post-check failed", "job_id", jobID, "err", err.Error())
			}
		}
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
	o.syncNetBoxLifecycle(context.Background(), job.DeviceID, to)

	o.mu.Lock()
	delete(o.running, jobID)
	o.mu.Unlock()
	return nil
}

// postCheckClean verifies media ejected and boot override cleared after cleanup.
func (o *Orchestrator) postCheckClean(ctx context.Context, bmcURL, credRef, systemID string) error {
	user, pass := "", ""
	if credRef != "" && o.secrets != nil {
		if c, err := o.secrets.Get(ctx, credRef); err == nil {
			user, pass = c.Username, c.Password
		}
	}
	bmc, err := o.newBMC(redfish.Config{
		BaseURL: bmcURL, Username: user, Password: pass,
		AuthMode: o.authMode, TLSMode: o.tlsMode, CAFile: o.caFile, MaxConcurrent: 1,
	})
	if err != nil {
		return fmt.Errorf("bmc for post-check: %w", err)
	}
	if err := bmc.Open(ctx); err != nil {
		return fmt.Errorf("bmc open post-check: %w", err)
	}
	defer func() { _ = bmc.Close(context.Background()) }()
	vms, err := bmc.ListVirtualMedia(ctx, systemID)
	if err != nil {
		return fmt.Errorf("list media post-check: %w", err)
	}
	for _, vm := range vms {
		if vm.Inserted {
			return fmt.Errorf("virtual media still inserted (%s)", vm.URI)
		}
	}
	boot, err := bmc.GetBoot(ctx, systemID)
	if err != nil {
		return fmt.Errorf("get boot post-check: %w", err)
	}
	if boot.OverrideEnabled != "" && boot.OverrideEnabled != "Disabled" && boot.OverrideEnabled != "disabled" {
		return fmt.Errorf("boot override still set (%s/%s)", boot.OverrideEnabled, boot.OverrideTarget)
	}
	return nil
}

// checkProfileApproval enforces Phase 5b human gate before any BMC action.
// spike / empty ref with no store entry is always allowed (Phase 2 path).
func (o *Orchestrator) checkProfileApproval(ctx context.Context, ref string, approveDestruct bool) error {
	if ref == "" || ref == "spike" {
		return nil
	}
	if o.profiles == nil {
		return fmt.Errorf("job: profile %q requires SHOAL_PROFILE_DIR (profile store not configured)", ref)
	}
	rec, err := o.profiles.Get(ctx, ref)
	if err != nil {
		return fmt.Errorf("job: load profile %q: %w", ref, err)
	}
	if !rec.NeedsOperatorApproval() {
		return nil
	}
	if approveDestruct {
		o.log.Info("profile destruct approved via StartJobRequest",
			"profile_ref", ref,
			"approved_by_flag", true,
		)
		return nil
	}
	return fmt.Errorf("job: profile %q requires approval (run: shoal profile approve -ref %s, or pass -approve-destruct)", ref, ref)
}

// resolveISOURL fills req.ISOURL from the profile store when the operator omitted -iso-url.
func (o *Orchestrator) resolveISOURL(ctx context.Context, req *models.StartJobRequest, profileRef string) error {
	if req.ISOURL != "" {
		return nil
	}
	if profileRef == "" || profileRef == "spike" {
		return fmt.Errorf("job: iso_url is required for spike/empty profile_ref")
	}
	if o.profiles == nil {
		return fmt.Errorf("job: cannot resolve iso_url without profile store (set SHOAL_PROFILE_DIR or pass -iso-url)")
	}
	rec, err := o.profiles.Get(ctx, profileRef)
	if err != nil {
		return fmt.Errorf("job: load profile %q for iso resolve: %w", profileRef, err)
	}
	url, err := iso.ResolveFromProfile(rec.Profile.ISOBase, o.isoBaseURL)
	if err != nil {
		return fmt.Errorf("job: resolve iso from profile %q: %w (set SHOAL_ISO_BASE_URL or pass -iso-url)", profileRef, err)
	}
	req.ISOURL = url
	o.log.Info("iso resolved from profile",
		"profile_ref", profileRef,
		"iso_base", rec.Profile.ISOBase,
		"iso_url", url,
	)
	return nil
}

// maybeBuildISO builds and publishes a live image when BuildISO is set, or when
// ISODynamic is enabled and ISOURL is still empty.
func (o *Orchestrator) maybeBuildISO(ctx context.Context, req *models.StartJobRequest, profileRef string) error {
	want := req.BuildISO || (o.isoDynamic && req.ISOURL == "")
	if !want {
		return nil
	}
	// Explicit URL without BuildISO: skip rebuild.
	if req.ISOURL != "" && !req.BuildISO {
		return nil
	}
	if o.isoBuilder == nil {
		if req.BuildISO {
			return fmt.Errorf("job: build_iso requested but ISO builder not configured")
		}
		return nil
	}
	if o.isoPublishDir == "" || o.isoBaseURL == "" {
		return fmt.Errorf("job: dynamic ISO requires SHOAL_ISO_PUBLISH_DIR and SHOAL_ISO_BASE_URL")
	}

	name := "shoal-install.iso"
	mode := strings.TrimSpace(req.ISOInstallMode)
	payloadFile := strings.TrimSpace(req.ISOPayloadFile)
	target := strings.TrimSpace(req.ISOInstallTarget)
	embedded := ""

	if profileRef != "" && profileRef != "spike" && o.profiles != nil {
		if rec, err := o.profiles.Get(ctx, profileRef); err == nil {
			if rec.Profile.ISOBase != "" {
				base := rec.Profile.ISOBase
				// Basename only; strip URL path if operator put a full URL in iso_base.
				if i := strings.LastIndex(base, "/"); i >= 0 {
					base = base[i+1:]
				}
				if !strings.HasSuffix(strings.ToLower(base), ".iso") {
					base += ".iso"
				}
				name = base
			}
			if payloadFile == "" && rec.Profile.EmbeddedPayload != "" {
				embedded = rec.Profile.EmbeddedPayload
			}
		}
	}
	if mode == "" {
		if payloadFile != "" || embedded != "" {
			mode = iso.InstallModeWrite
		} else {
			mode = iso.InstallModeSimulate
		}
	}

	in := iso.BuildInput{
		Name:            name,
		PayloadFile:     payloadFile,
		EmbeddedPayload: embedded,
		InstallMode:     mode,
		InstallTarget:   target,
	}
	url, art, err := buildAndPublish(ctx, o.isoBuilder, in, iso.PublishDest{
		Dir: o.isoPublishDir, BaseURL: o.isoBaseURL,
	})
	if err != nil {
		return fmt.Errorf("job: dynamic iso build: %w", err)
	}
	req.ISOURL = url
	o.log.Info("iso built dynamically",
		"profile_ref", profileRef,
		"path", art.Path,
		"size", art.Size,
		"mode", mode,
		"iso_url", url,
	)
	return nil
}

// buildAndPublish uses ScriptBuilder.BuildAndPublish when available, else Build+Publish.
func buildAndPublish(ctx context.Context, b iso.Builder, in iso.BuildInput, dest iso.PublishDest) (string, iso.Artifact, error) {
	if sb, ok := b.(*iso.ScriptBuilder); ok {
		return sb.BuildAndPublish(ctx, in, dest)
	}
	art, err := b.Build(ctx, in)
	if err != nil {
		return "", iso.Artifact{}, err
	}
	url, err := b.Publish(ctx, art, dest)
	return url, art, err
}

// syncNetBoxLifecycle best-effort updates NetBox identity lifecycle_state.
// Failures are logged only (do not reverse JobStore transition).
func (o *Orchestrator) syncNetBoxLifecycle(ctx context.Context, deviceKey string, state models.LifecycleState) {
	if o.netbox == nil || deviceKey == "" {
		return
	}
	if err := o.netbox.SetLifecycle(ctx, deviceKey, state); err != nil {
		o.log.Warn("netbox lifecycle sync failed",
			"device_id", deviceKey,
			"lifecycle_state", string(state),
			"err", err.Error(),
		)
		return
	}
	o.log.Info("netbox lifecycle synced", "device_id", deviceKey, "lifecycle_state", string(state))
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
// Re-attach SOL is deferred (MVP: fail orphans is the safe default).
func (o *Orchestrator) ReconcileOrphans(ctx context.Context) error {
	jobs, err := o.store.ListByState(ctx, models.StateProvisioning)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if !o.failOrphan {
			o.log.Warn("reconciling orphan job",
				"job_id", j.ID, "device_id", j.DeviceID,
				"decision", "skip", "reason", "SHOAL_RECONCILE_FAIL_ORPHANS=false",
			)
			continue
		}
		o.log.Warn("reconciling orphan job",
			"job_id", j.ID, "device_id", j.DeviceID,
			"decision", "fail_orphan", "reason", "unrecoverable_after_restart",
		)
		// Seed runState from durable job fields for cleanup coordinates.
		o.mu.Lock()
		if _, ok := o.running[j.ID]; !ok {
			o.running[j.ID] = &runState{
				bmcURL:     j.BMCEndpoint,
				sessionID:  j.SOLSessionID,
				systemID:   j.SystemID,
				credential: j.CredentialRef,
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
	// First terminal reason wins for in-process jobs. Observe may still report
	// stream close after cancel/DONE unregister; those must not replace the reason.
	o.mu.Lock()
	if rs, ok := o.running[jobID]; ok {
		if rs.terminalQueued {
			o.mu.Unlock()
			return
		}
		rs.terminalQueued = true
	}
	o.mu.Unlock()

	select {
	case o.terminal <- terminalEvent{jobID: jobID, reason: reason}:
	default:
		// channel full — run async
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), TerminalWorkerTimeout)
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
