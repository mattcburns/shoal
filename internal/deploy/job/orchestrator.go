// Package job implements the Deploy Orchestrator, HandleTerminal, and jobport adapter.
// Orchestrator is the sole lifecycle writer.
package job

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mattcburns/shoal/internal/common/jobport"
	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/netbox"
	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/common/secrets"
	"github.com/mattcburns/shoal/internal/common/telemetry"
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
	// DefaultOperatorStageTimeout is the coarse wait for operator_iso when StageTimeout is 0.
	DefaultOperatorStageTimeout = 60 * time.Minute
)

// Orchestrator owns lifecycle transitions, BMC actions, and cleanup.
type Orchestrator struct {
	log                    *slog.Logger
	store                  jobstore.Store
	secrets                secrets.Backend
	newBMC                 redfish.Factory
	watches                watchport.WatchRegistrar
	netbox                 netbox.LifecycleWriter // optional; identity lifecycle only
	resolver               netbox.DeviceResolver  // optional; remap name/serial → NetBox pk
	telemetry              telemetry.Store        // optional; durable job_log from SOL markers
	profiles               profile.Store          // optional; Phase 5b approval gate
	isoBaseURL             string                 // Phase 5c: resolve profile iso_base → URL
	isoBuilder             iso.Builder            // optional Phase 6a dynamic build
	isoPublishDir          string
	isoDynamic             bool // SHOAL_ISO_DYNAMIC: build when ISOURL empty + publish configured
	authMode               string
	tlsMode                string
	caFile                 string
	failOrphan             bool
	defaultSerialTransport string
	// defaultBMCUser/Pass fill Start when the request omits credentials (lab +
	// NetBox plugin start without shipping passwords through the UI).
	defaultBMCUser string
	defaultBMCPass string

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
	// serialTarget / stallTimeout / deviceID retained for stage advance (M2).
	serialTarget string
	stallTimeout time.Duration
	deviceID     string
	// req is the original start request (needed to re-enter startStage after prep).
	// Credentials live in secrets backend; password fields must never be logged.
	req models.StartJobRequest
	// terminalQueued is set under Orchestrator.mu so only the first terminal
	// reason (cancel, DONE, stall, transport, …) is accepted. Without this,
	// Unregister-driven stream close races cancel and can win with "sol transport error".
	terminalQueued bool
	// terminalOnce ensures HandleTerminal runs once
	terminalOnce sync.Once
	// advanceOnce ensures PREP_DONE only advances once.
	advanceOnce sync.Once
	// stageStarted is when the current stage's SOL watch was registered.
	// Early stream closes (domain reboot after media swap) are retried.
	stageStarted  time.Time
	solReopens    int
	maxSOLReopens int
	// progressCoarse: M5 operator_iso — no SOL stall; stage deadline → provisioned.
	progressCoarse bool
	// stopDeadline cancels the coarse stage timer (also called from HandleTerminal).
	stopDeadline func()
}

type terminalEvent struct {
	jobID  string
	reason TerminalReason
}

// Options configures the Orchestrator.
type Options struct {
	Log     *slog.Logger
	Store   jobstore.Store
	Secrets secrets.Backend
	NewBMC  redfish.Factory
	Watches watchport.WatchRegistrar
	NetBox  netbox.LifecycleWriter // optional Phase 5 lifecycle sync
	// DeviceResolver remaps device_id (name/serial → NetBox pk) at Start.
	// When nil and NetBox implements DeviceResolver, NetBox is used.
	DeviceResolver netbox.DeviceResolver
	// Telemetry is optional; when set, SOL markers are appended to job_log
	// for GET /v1/jobs/{id}/log and the NetBox Jobs tab.
	Telemetry           telemetry.Store
	Profiles            profile.Store // optional Phase 5b profile load/approval
	ISOBaseURL          string        // optional Phase 5c profile → ISO URL resolve
	ISOBuilder          iso.Builder   // optional Phase 6a dynamic build
	ISOPublishDir       string        // publish dir for dynamic build
	ISODynamic          bool          // build when ISOURL empty (needs builder+publish+base)
	AuthMode            string
	TLSMode             string
	CAFile              string
	ReconcileFailOrphan bool
	// DefaultSerialTransport is the orchestrator-wide serial transport
	// ("libvirt" | "redfish_sol") used when StartJobRequest.SerialTransport is
	// empty. Empty here means "libvirt" (unchanged behavior).
	DefaultSerialTransport string
	// DefaultBMCUsername/Password fill empty Start credentials from env
	// (SHOAL_BMC_*), so NetBox start-job need not post passwords.
	DefaultBMCUsername string
	DefaultBMCPassword string
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
	resolver := opts.DeviceResolver
	if resolver == nil {
		if r, ok := opts.NetBox.(netbox.DeviceResolver); ok {
			resolver = r
		}
	}
	o := &Orchestrator{
		log:                    log,
		store:                  opts.Store,
		secrets:                opts.Secrets,
		newBMC:                 opts.NewBMC,
		watches:                opts.Watches,
		netbox:                 opts.NetBox,
		resolver:               resolver,
		telemetry:              opts.Telemetry,
		profiles:               opts.Profiles,
		isoBaseURL:             opts.ISOBaseURL,
		isoBuilder:             opts.ISOBuilder,
		isoPublishDir:          opts.ISOPublishDir,
		isoDynamic:             opts.ISODynamic,
		authMode:               auth,
		tlsMode:                tlsMode,
		caFile:                 opts.CAFile,
		failOrphan:             opts.ReconcileFailOrphan, // composition root passes config default true
		defaultSerialTransport: opts.DefaultSerialTransport,
		defaultBMCUser:         opts.DefaultBMCUsername,
		defaultBMCPass:         opts.DefaultBMCPassword,
		running:                make(map[string]*runState),
		terminal:               make(chan terminalEvent, 64),
		stopCh:                 make(chan struct{}),
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
			// A panic in terminal handling must not kill the whole process
			// (Compose would restart and orphan-fail an otherwise-successful job).
			func() {
				defer func() {
					if r := recover(); r != nil {
						o.log.Error("HandleTerminal panic recovered",
							"job_id", ev.jobID,
							"reason", string(ev.reason),
							"recover", fmt.Sprint(r),
						)
					}
				}()
				ctx, cancel := context.WithTimeout(context.Background(), TerminalWorkerTimeout)
				defer cancel()
				if err := o.HandleTerminal(ctx, ev.jobID, ev.reason); err != nil {
					o.log.Error("HandleTerminal failed", "job_id", ev.jobID, "reason", string(ev.reason), "err", err.Error())
				}
			}()
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
// M6: non-spike profiles fill empty strategy/prep/seed/family/media fields before validate.
// When ISOURL is empty, media_url / iso_base resolve via SHOAL_ISO_BASE_URL (Phase 5c).
// Optional BuildISO / SHOAL_ISO_DYNAMIC builds and publishes a live image first (Phase 6a).
// When a DeviceResolver is available (NetBox), device_id is remapped from name/serial
// to the NetBox numeric primary key so plugin tabs and telemetry share one key.
func (o *Orchestrator) Start(ctx context.Context, req models.StartJobRequest) (models.ProvisioningJob, error) {
	if o.watches == nil {
		return models.ProvisioningJob{}, fmt.Errorf("job: watch registrar not configured")
	}

	profileRef := req.ProfileRef
	if profileRef == "" {
		profileRef = "spike"
	}
	req.ProfileRef = profileRef

	if err := o.resolveDeviceID(ctx, &req); err != nil {
		return models.ProvisioningJob{}, err
	}
	o.applyDefaultCredentials(&req)

	// M6: apply profile defaults before validation so profile-only starts work.
	var prof models.ProvisioningProfile
	if profileRef != "" && profileRef != "spike" {
		if o.profiles == nil {
			return models.ProvisioningJob{}, fmt.Errorf("job: profile %q requires SHOAL_PROFILE_DIR (profile store not configured)", profileRef)
		}
		rec, err := o.profiles.Get(ctx, profileRef)
		if err != nil {
			return models.ProvisioningJob{}, fmt.Errorf("job: load profile %q: %w", profileRef, err)
		}
		prof = rec.Profile
		applyProfileDefaults(&req, prof)
		if err := resolveProfileURLs(&req, prof, o.isoBaseURL); err != nil {
			return models.ProvisioningJob{}, err
		}
	}

	if err := validate.StartJobRequest(req); err != nil {
		return models.ProvisioningJob{}, err
	}
	if err := o.checkProfileApproval(ctx, profileRef, req.ApproveDestruct); err != nil {
		return models.ProvisioningJob{}, err
	}
	if err := o.maybeBuildISO(ctx, &req, profileRef); err != nil {
		return models.ProvisioningJob{}, err
	}
	// Legacy resolve if still empty (iso_base only path when media_url not used).
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

	// Probe CD count early so seed_delivery=auto can choose second_media vs config_drive.
	nCD := o.probeCDCount(ctx, req, cred)
	stages, err := expandStages(req, nCD)
	if err != nil {
		return models.ProvisioningJob{}, err
	}
	strategy := installStrategyFromStages(stages)

	profile := profileRef
	now := time.Now().UTC()
	sessionID := "sol-" + jobID
	job := models.ProvisioningJob{
		ID:              jobID,
		DeviceID:        req.DeviceID,
		ProfileRef:      profile,
		State:           models.StateProvisioning,
		Attempt:         1,
		Phase:           "STARTING",
		StartedAt:       &now,
		UpdatedAt:       &now,
		ISOURL:          req.ISOURL,
		BMCEndpoint:     req.BMCEndpoint,
		SystemID:        req.SystemID,
		CredentialRef:   credRef,
		SOLSessionID:    sessionID,
		InstallStrategy: strategy,
		Stages:          stages,
	}
	if err := o.store.Insert(ctx, job); err != nil {
		return models.ProvisioningJob{}, err
	}
	o.syncNetBoxLifecycle(ctx, req.DeviceID, models.StateProvisioning)

	jobCtx, cancel := context.WithCancel(context.Background())
	stall := req.StallTimeout
	if stall <= 0 {
		stall = DefaultSOLStall
	}
	rs := &runState{
		cancel:       cancel,
		systemID:     req.SystemID,
		credential:   credRef,
		bmcURL:       req.BMCEndpoint,
		sessionID:    sessionID,
		serialTarget: req.SerialTarget,
		stallTimeout: stall,
		deviceID:     req.DeviceID,
		req:          req,
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

// provision expands stages and starts only the first stage. Later stages start
// when ApplyMarker sees PREP_DONE (M2 event-driven advance).
func (o *Orchestrator) provision(ctx context.Context, job models.ProvisioningJob, req models.StartJobRequest, cred secrets.Credential, rs *runState) error {
	stages := job.Stages
	if len(stages) == 0 {
		var err error
		stages, err = expandStages(req, 1)
		if err != nil {
			return err
		}
	}
	strategy := job.InstallStrategy
	if strategy == "" {
		strategy = installStrategyFromStages(stages)
	}
	if err := o.store.UpdateStages(ctx, job.ID, stages[0].ID, strategy, stages); err != nil {
		return fmt.Errorf("persist stages: %w", err)
	}
	job.Stages = stages
	job.CurrentStage = stages[0].ID
	job.InstallStrategy = strategy
	return o.startStage(ctx, job, req, cred, rs, 0)
}

// startStage attaches stage media, boots CD once, and registers SOL watch.
func (o *Orchestrator) startStage(ctx context.Context, job models.ProvisioningJob, req models.StartJobRequest, cred secrets.Credential, rs *runState, idx int) error {
	if idx < 0 || idx >= len(job.Stages) {
		return fmt.Errorf("job: invalid stage index %d", idx)
	}
	stage := job.Stages[idx]
	strategy := job.InstallStrategy
	stages := setStageState(job.Stages, stage.ID, models.JobStageStateRunning, "STARTING", "")
	if err := o.store.UpdateStages(ctx, job.ID, stage.ID, strategy, stages); err != nil {
		return fmt.Errorf("persist stages: %w", err)
	}
	o.log.Info("stage start",
		"job_id", job.ID,
		"stage", stage.ID,
		"kind", stage.Kind,
		"strategy", stage.Strategy,
	)

	mediaURL := strings.TrimSpace(stage.MediaURL)
	if mediaURL == "" && stage.Kind == models.JobStageKindOSInstall {
		mediaURL = strings.TrimSpace(req.ISOURL)
	}
	if mediaURL == "" {
		return fmt.Errorf("%s: media_url is empty", stage.Kind)
	}

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

	// Prefer explicit SystemID, then serial target / name (lab multi-system sushy),
	// then device_id. After NetBox resolve, device_id is a numeric pk and is a
	// poor Redfish system key — serial_target (shoal-node-1) still matches Name.
	lookup := req.SystemID
	if lookup == "" {
		lookup = req.SerialTarget
	}
	if lookup == "" {
		lookup = req.DeviceID
	}
	sys, err := bmc.GetSystem(ctx, lookup)
	if err != nil {
		if req.SystemID == "" {
			sys, err = bmc.GetSystem(ctx, "")
		}
		if err != nil {
			return fmt.Errorf("get system: %w", err)
		}
	}
	rs.systemID = sys.ID
	if err := o.store.UpdateRuntime(ctx, job.ID, sys.ID, rs.sessionID, rs.credential); err != nil {
		return fmt.Errorf("persist runtime: %w", err)
	}

	// Best-effort eject previous media before insert (stage handoff).
	_ = bmc.CleanupMediaAndBoot(ctx, sys.ID)

	vms, err := bmc.ListVirtualMedia(ctx, sys.ID)
	if err != nil {
		return fmt.Errorf("list virtual media: %w", err)
	}
	primaryURI, secondaryURI := pickCDMediaPair(vms)
	if primaryURI == "" {
		return fmt.Errorf("no CD-capable virtual media slot")
	}

	// M3: resolve offline seed and optionally attach second_media seed ISO.
	seedDelivery := models.SeedDeliveryNone
	seedURL := ""
	if stage.Kind == models.JobStageKindOSInstall {
		seedURL = strings.TrimSpace(stage.SeedMediaURL)
		if seedURL == "" {
			seedURL = strings.TrimSpace(req.SeedISOURL)
		}
		if seedURL == "" {
			seedURL = strings.TrimSpace(os.Getenv("SHOAL_SEED_ISO_URL"))
		}
		requested := stage.SeedDelivery
		if requested == "" {
			requested = req.SeedDelivery
		}
		stageStrategy := stage.Strategy
		if stageStrategy == "" {
			stageStrategy = strategy
		}
		var resErr error
		seedDelivery, resErr = resolveSeedDelivery(requested, seedURL, stageStrategy, countCDMedia(vms))
		if resErr != nil {
			return resErr
		}
		if seedDelivery == models.SeedDeliverySecondMedia {
			if secondaryURI == "" {
				return fmt.Errorf("job: second_media resolved but no secondary CD slot")
			}
			if seedURL == "" {
				return fmt.Errorf("job: second_media requires seed ISO URL")
			}
		}
		// Persist resolved delivery on the stage for job status.
		stages = setStageSeed(stages, stage.ID, seedDelivery, seedURL)
		_ = o.store.UpdateStages(ctx, job.ID, stage.ID, strategy, stages)
	}

	if err := bmc.InsertVirtualMedia(ctx, primaryURI, mediaURL); err != nil {
		return fmt.Errorf("insert media: %w", err)
	}
	if seedDelivery == models.SeedDeliverySecondMedia {
		if err := bmc.InsertVirtualMedia(ctx, secondaryURI, seedURL); err != nil {
			return fmt.Errorf("insert seed media: %w", err)
		}
		o.log.Info("second_media seed attached",
			"job_id", job.ID,
			"install_media", primaryURI,
			"seed_media", secondaryURI,
		)
	}
	if err := bmc.SetBootOverrideOnceCD(ctx, sys.ID); err != nil {
		return fmt.Errorf("boot override: %w", err)
	}
	if err := bmc.Power(ctx, sys.ID, "ForceRestart"); err != nil {
		if err2 := bmc.Power(ctx, sys.ID, "On"); err2 != nil {
			return fmt.Errorf("power on/restart: %w (also: %v)", err2, err)
		}
	}
	// After media swap / ForceRestart, give nested serial a moment to settle.
	settle := 2 * time.Second
	if idx > 0 {
		settle = 8 * time.Second
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(settle):
	}

	// Progress policy: operator_iso and scripted_iso use coarse deadline (no SOL stall).
	stageStrategy := stage.Strategy
	if stageStrategy == "" {
		stageStrategy = strategy
	}
	coarse := stage.Kind == models.JobStageKindOSInstall &&
		(stageStrategy == models.InstallStrategyOperatorISO || stageStrategy == models.InstallStrategyScriptedISO)
	rs.progressCoarse = coarse
	if rs.stopDeadline != nil {
		rs.stopDeadline()
		rs.stopDeadline = nil
	}

	phase := "WAITING_SOL"
	if stage.Kind == models.JobStageKindPrep {
		phase = "PREP_BOOT"
	}
	if coarse {
		phase = "WAITING_INSTALL"
	}
	_ = o.store.UpdateProgress(ctx, job.ID, phase, nil, 0, "")
	stages = setStageState(stages, stage.ID, models.JobStageStateRunning, phase, "")
	_ = o.store.UpdateStages(ctx, job.ID, stage.ID, strategy, stages)

	// New session id per stage so Unregister of previous stage does not race.
	sessionID := rs.sessionID
	if idx > 0 {
		sessionID = "sol-" + job.ID + "-s" + fmt.Sprintf("%d", idx)
		rs.sessionID = sessionID
		_ = o.store.UpdateRuntime(ctx, job.ID, sys.ID, sessionID, rs.credential)
	}

	transport := o.resolveSerialTransport(req)
	serialTarget := strings.TrimSpace(req.SerialTarget)
	target := serialTarget
	redfishSystemID := ""
	credRef := ""
	switch transport {
	case "redfish_sol":
		target = req.BMCEndpoint
		redfishSystemID = sys.ID
		credRef = rs.credential
	case "libvirt", "":
		if serialTarget == "" && !coarse {
			_ = bmc.CleanupMediaAndBoot(ctx, sys.ID)
			return fmt.Errorf("serial_target is required for marker-based stages")
		}
	default:
		_ = bmc.CleanupMediaAndBoot(ctx, sys.ID)
		return fmt.Errorf("job: unsupported serial_transport %q", transport)
	}
	if target != "" {
		stall := rs.stallTimeout
		if stall <= 0 {
			stall = DefaultSOLStall
		}
		session := models.WatchSession{
			ID:              sessionID,
			JobID:           job.ID,
			DeviceID:        req.DeviceID,
			Transport:       transport,
			Target:          target,
			RedfishSystemID: redfishSystemID,
			CredentialRef:   credRef,
			StartedAt:       time.Now().UTC(),
			StallTimeout:    stall,
			StallDisabled:   coarse,
		}
		if err := o.watches.Register(ctx, session); err != nil {
			_ = bmc.CleanupMediaAndBoot(ctx, sys.ID)
			return fmt.Errorf("register watch: %w", err)
		}
	}
	rs.stageStarted = time.Now().UTC()
	rs.solReopens = 0
	if rs.maxSOLReopens == 0 {
		rs.maxSOLReopens = 4
	}

	if coarse {
		deadline := req.StageTimeout
		if deadline <= 0 {
			for _, envKey := range []string{"SHOAL_COARSE_STAGE_TIMEOUT", "SHOAL_OPERATOR_ISO_TIMEOUT", "SHOAL_SCRIPTED_ISO_TIMEOUT"} {
				if env := strings.TrimSpace(os.Getenv(envKey)); env != "" {
					if d, err := time.ParseDuration(env); err == nil && d > 0 {
						deadline = d
						break
					}
				}
			}
		}
		if deadline <= 0 {
			deadline = DefaultOperatorStageTimeout
		}
		o.armCoarseDeadline(job.ID, rs, deadline, stageStrategy)
	}

	o.log.Info("stage media attached",
		"job_id", job.ID,
		"device_id", job.DeviceID,
		"bmc", req.BMCEndpoint,
		"iso_url", mediaURL,
		"stage", stage.ID,
		"kind", stage.Kind,
		"strategy", stageStrategy,
		"family", stage.Family,
		"coarse", coarse,
	)
	return nil
}

// armCoarseDeadline schedules optimistic provisioned when a coarse stage timeout elapses.
func (o *Orchestrator) armCoarseDeadline(jobID string, rs *runState, deadline time.Duration, strategy string) {
	timer := time.NewTimer(deadline)
	done := make(chan struct{})
	rs.stopDeadline = func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		select {
		case <-done:
		default:
			close(done)
		}
	}
	o.log.Info("coarse stage deadline armed",
		"job_id", jobID,
		"strategy", strategy,
		"deadline", deadline.String(),
	)
	go func() {
		select {
		case <-done:
			return
		case <-timer.C:
		case <-o.stopCh:
			return
		}
		// Mark phase then terminal success (not install verification).
		detail := strategy + ": stage deadline elapsed (no SOL verify)"
		_ = o.store.UpdateProgress(context.Background(), jobID, "COARSE_DONE", intPtr(100), 0, detail)
		o.enqueueTerminal(jobID, ReasonDoneOK)
	}()
}

func intPtr(n int) *int { return &n }

// reopenSOL re-registers the SOL watch after an early stream drop (stage handoff).
// resolveSerialTransport picks the serial transport for a job: the per-job
// StartJobRequest.SerialTransport override wins when set, else the
// orchestrator-wide default (Options.DefaultSerialTransport /
// SHOAL_SERIAL_TRANSPORT), else "libvirt" (unchanged existing behavior).
func (o *Orchestrator) resolveSerialTransport(req models.StartJobRequest) string {
	if t := strings.TrimSpace(req.SerialTransport); t != "" {
		return t
	}
	if o.defaultSerialTransport != "" {
		return o.defaultSerialTransport
	}
	return "libvirt"
}

func (o *Orchestrator) reopenSOL(jobID string) {
	o.mu.Lock()
	rs := o.running[jobID]
	o.mu.Unlock()
	if rs == nil {
		return
	}
	if rs.terminalQueued {
		return
	}
	job, err := o.store.Get(context.Background(), jobID)
	if err != nil || job.State != models.StateProvisioning {
		return
	}
	// Unregister current session if any.
	if rs.sessionID != "" && o.watches != nil {
		_ = o.watches.Unregister(context.Background(), rs.sessionID)
	}
	time.Sleep(4 * time.Second)
	if rs.terminalQueued {
		return
	}
	rs.solReopens++
	sessionID := fmt.Sprintf("sol-%s-r%d", jobID, rs.solReopens)
	rs.sessionID = sessionID
	_ = o.store.UpdateRuntime(context.Background(), jobID, rs.systemID, sessionID, rs.credential)
	stall := rs.stallTimeout
	if stall <= 0 {
		stall = DefaultSOLStall
	}
	transport := o.resolveSerialTransport(rs.req)
	target := rs.serialTarget
	redfishSystemID := ""
	credRef := ""
	if transport == "redfish_sol" {
		target = rs.bmcURL
		redfishSystemID = rs.systemID
		credRef = rs.credential
	}
	session := models.WatchSession{
		ID:              sessionID,
		JobID:           jobID,
		DeviceID:        rs.deviceID,
		Transport:       transport,
		Target:          target,
		RedfishSystemID: redfishSystemID,
		CredentialRef:   credRef,
		StartedAt:       time.Now().UTC(),
		StallTimeout:    stall,
	}
	if err := o.watches.Register(context.Background(), session); err != nil {
		o.log.Error("sol reopen failed", "job_id", jobID, "err", err.Error())
		o.enqueueTerminal(jobID, ReasonTransport)
		return
	}
	rs.stageStarted = time.Now().UTC()
	o.log.Info("sol watch reopened after stream drop",
		"job_id", jobID,
		"session_id", sessionID,
		"reopen", rs.solReopens,
	)
}

// advanceAfterPrepDone completes the prep stage and starts os_install (M2).
func (o *Orchestrator) advanceAfterPrepDone(jobID string) {
	o.mu.Lock()
	rs := o.running[jobID]
	o.mu.Unlock()
	if rs == nil {
		o.log.Warn("prep advance: no runState", "job_id", jobID)
		return
	}
	var run bool
	rs.advanceOnce.Do(func() { run = true })
	if !run {
		return
	}

	ctx := context.Background()
	job, err := o.store.Get(ctx, jobID)
	if err != nil {
		o.log.Error("prep advance get job", "job_id", jobID, "err", err.Error())
		return
	}
	if job.CurrentStage != models.JobStageKindPrep && job.CurrentStage != "" {
		// Allow empty current during race; still require a prep stage present.
		if stageIndex(job.Stages, models.JobStageKindPrep) < 0 {
			return
		}
	}
	if job.State != models.StateProvisioning {
		return
	}

	// Unregister prep SOL session.
	if rs.sessionID != "" && o.watches != nil {
		_ = o.watches.Unregister(ctx, rs.sessionID)
	}

	// Mark prep done.
	stages := setStageState(job.Stages, models.JobStageKindPrep, models.JobStageStateDone, "PREP_DONE", "")
	osIdx := stageIndex(stages, models.JobStageKindOSInstall)
	if osIdx < 0 {
		o.enqueueTerminal(jobID, ReasonMarkerError)
		return
	}
	_ = o.store.UpdateStages(ctx, jobID, models.JobStageKindOSInstall, job.InstallStrategy, stages)
	// Clear soft error from prior progress; phase only for observability.
	_ = o.store.UpdateProgress(ctx, jobID, "STAGE_ADVANCE", nil, 0, "")

	cred, err := o.secrets.Get(ctx, rs.credential)
	if err != nil {
		o.log.Error("prep advance secrets", "job_id", jobID, "err", err.Error())
		o.enqueueTerminal(jobID, ReasonBMC)
		return
	}
	job.Stages = stages
	job.CurrentStage = models.JobStageKindOSInstall
	req := rs.req
	if err := o.startStage(ctx, job, req, cred, rs, osIdx); err != nil {
		o.log.Error("os_install start after prep failed", "job_id", jobID, "err", err.Error())
		stages = setStageState(stages, models.JobStageKindOSInstall, models.JobStageStateFailed, "ERROR", err.Error())
		_ = o.store.UpdateStages(ctx, jobID, models.JobStageKindOSInstall, job.InstallStrategy, stages)
		o.enqueueTerminal(jobID, ReasonBMC)
	}
}

// pickCDMediaPair returns primary (boot) and secondary CD-capable media URIs.
// URIs are sorted so selection is stable across Fake map iteration order.
func pickCDMediaPair(vms []redfish.VirtualMedia) (primary, secondary string) {
	var cds []string
	for _, vm := range vms {
		if vm.SupportsCD {
			cds = append(cds, vm.URI)
		}
	}
	sort.Strings(cds)
	if len(cds) == 0 {
		if len(vms) > 0 {
			return vms[0].URI, ""
		}
		return "", ""
	}
	if len(cds) == 1 {
		return cds[0], ""
	}
	return cds[0], cds[1]
}

func countCDMedia(vms []redfish.VirtualMedia) int {
	n := 0
	for _, vm := range vms {
		if vm.SupportsCD {
			n++
		}
	}
	return n
}

// setStageSeed updates seed fields on the stage with id.
func setStageSeed(stages []models.JobStage, id, delivery, seedURL string) []models.JobStage {
	out := make([]models.JobStage, len(stages))
	copy(out, stages)
	for i := range out {
		if out[i].ID == id {
			out[i].SeedDelivery = delivery
			if seedURL != "" {
				out[i].SeedMediaURL = seedURL
			}
			break
		}
	}
	return out
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

	if rs != nil {
		if rs.stopDeadline != nil {
			rs.stopDeadline()
			rs.stopDeadline = nil
		}
		if rs.cancel != nil {
			rs.cancel()
		}
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
		// SOL DONE means the install path succeeded. Cleanup remains mandatory
		// best-effort, but must not reverse success: process crash, sushy tray
		// removal, or post-restart secret loss must not mark a finished install failed.
		to = models.StateProvisioned
		errMsg = ""
		if cleanupErr != nil {
			if cleanupAlreadyClean(cleanupErr) {
				o.log.Info("DONE cleanup treated as already clean", "job_id", jobID, "err", cleanupErr.Error())
			} else {
				o.log.Error("DONE cleanup incomplete (job still provisioned)",
					"job_id", jobID, "err", cleanupErr.Error())
			}
		}
		if bmcURL != "" {
			if err := o.postCheckClean(context.Background(), bmcURL, credRef, systemID); err != nil {
				o.log.Warn("DONE post-check warning (job still provisioned)",
					"job_id", jobID, "err", err.Error())
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

	// Mark current stage terminal for accurate job status (M1+).
	if len(job.Stages) > 0 {
		stageID := job.CurrentStage
		if stageID == "" {
			stageID = job.Stages[len(job.Stages)-1].ID
		}
		stageState := models.JobStageStateFailed
		stagePhase := job.Phase
		if to == models.StateProvisioned {
			stageState = models.JobStageStateDone
			if stagePhase == "" {
				stagePhase = "DONE"
			}
		}
		stages := setStageState(job.Stages, stageID, stageState, stagePhase, errMsg)
		_ = o.store.UpdateStages(context.Background(), jobID, stageID, job.InstallStrategy, stages)
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

// cleanupAlreadyClean reports cleanup errors that mean "nothing left to eject"
// (common on sushy after eject removes the CD device).
func cleanupAlreadyClean(err error) bool {
	if err == nil {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no virtual media") ||
		strings.Contains(msg, "no cd") ||
		(strings.Contains(msg, "not found") && strings.Contains(msg, "media"))
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
		// Empty/missing media inventory after sushy eject is already-clean.
		if cleanupAlreadyClean(err) {
			return nil
		}
		return fmt.Errorf("list media post-check: %w", err)
	}
	// No slots left ⇒ nothing inserted (lab fidelity after eject).
	for _, vm := range vms {
		if vm.Inserted {
			return fmt.Errorf("virtual media still inserted (%s)", vm.URI)
		}
	}
	boot, err := bmc.GetBoot(ctx, systemID)
	if err != nil {
		return fmt.Errorf("get boot post-check: %w", err)
	}
	// Clean = Disabled, empty, or sushy-tools steady state Continuous/Hdd
	// (ClearBootOverride falls back to Continuous+Hdd when Disabled is rejected).
	if !bootOverrideCleared(boot) {
		return fmt.Errorf("boot override still set (%s/%s)", boot.OverrideEnabled, boot.OverrideTarget)
	}
	return nil
}

// bootOverrideCleared reports whether the BMC boot source is in a post-cleanup state
// (no one-time CD/USB override left active).
func bootOverrideCleared(boot redfish.BootInfo) bool {
	en := strings.TrimSpace(boot.OverrideEnabled)
	tgt := strings.TrimSpace(boot.OverrideTarget)
	switch {
	case en == "" || strings.EqualFold(en, "Disabled"):
		return true
	case strings.EqualFold(en, "Continuous") &&
		(tgt == "" || strings.EqualFold(tgt, "None") || strings.EqualFold(tgt, "Hdd") || strings.EqualFold(tgt, "Disk") || strings.EqualFold(tgt, "Hd")):
		// sushy-tools maps "clear override" onto Continuous + Hdd boot order.
		return true
	default:
		return false
	}
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

// probeCDCount lists CD-capable Virtual Media slots (best-effort; defaults to 1).
func (o *Orchestrator) probeCDCount(ctx context.Context, req models.StartJobRequest, cred secrets.Credential) int {
	if o.newBMC == nil {
		return 1
	}
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
		return 1
	}
	if err := bmc.Open(ctx); err != nil {
		return 1
	}
	defer func() { _ = bmc.Close(context.Background()) }()
	lookup := req.SystemID
	if lookup == "" {
		lookup = req.SerialTarget
	}
	if lookup == "" {
		lookup = req.DeviceID
	}
	sys, err := bmc.GetSystem(ctx, lookup)
	if err != nil {
		sys, err = bmc.GetSystem(ctx, "")
		if err != nil {
			return 1
		}
	}
	vms, err := bmc.ListVirtualMedia(ctx, sys.ID)
	if err != nil {
		return 1
	}
	n := countCDMedia(vms)
	if n <= 0 {
		return 1
	}
	return n
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
	ubuntuBase := strings.TrimSpace(req.ISOUbuntuBase)
	hostname := strings.TrimSpace(req.ISOHostname)
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
		if ubuntuBase != "" {
			mode = iso.InstallModeAutoinstall
		} else if payloadFile != "" || embedded != "" {
			mode = iso.InstallModeWrite
		} else {
			mode = iso.InstallModeSimulate
		}
	}
	if mode == iso.InstallModeAutoinstall && name == "shoal-install.iso" {
		name = "shoal-ubuntu-autoinstall.iso"
	}

	in := iso.BuildInput{
		Name:            name,
		PayloadFile:     payloadFile,
		EmbeddedPayload: embedded,
		InstallMode:     mode,
		InstallTarget:   target,
		UbuntuBaseISO:   ubuntuBase,
		Hostname:        hostname,
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

// ReconcileOrphans finishes in-flight PROVISIONING jobs left from a previous process.
// Default policy (failOrphan=true): cleanup + terminal state for each.
// Jobs that already recorded a successful DONE marker (phase DONE / 100%) are
// completed as done_ok so a crash mid-cleanup does not erase a successful install.
// Other orphans fail (re-attach SOL is deferred; fail is the safe default).
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
		reason := ReasonBMC
		decision := "fail_orphan"
		why := "unrecoverable_after_restart"
		if orphanLooksSuccessfullyComplete(j) {
			// Process died after SOL DONE but before lifecycle commit — honor success.
			reason = ReasonDoneOK
			decision = "complete_orphan_done"
			why = "phase_done_after_restart"
		}
		o.log.Warn("reconciling orphan job",
			"job_id", j.ID, "device_id", j.DeviceID,
			"decision", decision, "reason", why,
			"phase", j.Phase, "percent", percentLog(j.Percent),
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
		if err := o.HandleTerminal(ctx, j.ID, reason); err != nil {
			o.log.Error("orphan reconcile failed", "job_id", j.ID, "err", err.Error())
		}
	}
	return nil
}

// orphanLooksSuccessfullyComplete reports whether a still-PROVISIONING job row
// already reflects a successful install (DONE marker applied) so restart should
// not fail the job.
func orphanLooksSuccessfullyComplete(j models.ProvisioningJob) bool {
	if strings.EqualFold(strings.TrimSpace(j.Phase), "DONE") {
		return true
	}
	if j.Percent != nil && *j.Percent >= 100 && j.LastMarkerSeq > 0 {
		// Percent 100 alone can appear mid-stage; require a marker seq and
		// a success-shaped phase when not exactly DONE.
		switch strings.ToUpper(strings.TrimSpace(j.Phase)) {
		case "DONE", "VERIFY", "COMPLETE", "FINISHED":
			return true
		}
	}
	return false
}

func percentLog(p *int) any {
	if p == nil {
		return nil
	}
	return *p
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
	p.orch.appendJobLog(ctx, jobID, m)
	// Best-effort stage phase mirror.
	if j, err := p.orch.store.Get(ctx, jobID); err == nil && j.CurrentStage != "" && len(j.Stages) > 0 {
		stages := setStageState(j.Stages, j.CurrentStage, models.JobStageStateRunning, phase, soft)
		_ = p.orch.store.UpdateStages(ctx, jobID, j.CurrentStage, j.InstallStrategy, stages)
	}

	// M2: PREP_DONE advances to os_install — not a job-terminal event.
	if strings.EqualFold(m.Phase, "PREP_DONE") && (m.State == "OK" || m.State == "") {
		job, err := p.orch.store.Get(ctx, jobID)
		if err == nil && (job.CurrentStage == models.JobStageKindPrep || stageIndex(job.Stages, models.JobStageKindPrep) >= 0) {
			// If DONE-shaped marker on prep only: advance when still on prep.
			if job.CurrentStage == models.JobStageKindPrep || job.CurrentStage == "" {
				go p.orch.advanceAfterPrepDone(jobID)
				return nil
			}
		}
	}

	if sol.IsTerminal(m) {
		// DONE during prep stage is a misbehaving image — fail the job.
		if strings.EqualFold(m.Phase, "DONE") {
			if j, err := p.orch.store.Get(ctx, jobID); err == nil && j.CurrentStage == models.JobStageKindPrep {
				_ = p.orch.store.UpdateProgress(ctx, jobID, "ERROR", nil, m.Seq, "DONE during prep stage")
				p.orch.enqueueTerminal(jobID, ReasonMarkerError)
				return nil
			}
		}
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
	p.orch.appendJobLog(ctx, jobID, models.SOLMarker{
		SchemaVer: sol.SchemaVersion,
		Timestamp: time.Now().UTC(),
		Phase:     "STALL",
		State:     "ERROR",
		Detail:    reason,
	})
	p.orch.enqueueTerminal(jobID, ReasonStall)
	return nil
}

func (p *progressAdapter) ReportTransportError(ctx context.Context, jobID string, err error) error {
	// Ignore stream close after DONE/cancel/stall already committed (e.g. guest poweroff).
	if !p.stillProvisioning(ctx, jobID) {
		return nil
	}
	// After stage handoff / ForceRestart, nested serial often drops once; reopen SOL.
	p.orch.mu.Lock()
	rs := p.orch.running[jobID]
	p.orch.mu.Unlock()
	// M5 operator_iso: SOL is best-effort; stream close must not fail the job.
	if rs != nil && rs.progressCoarse {
		p.orch.log.Info("sol stream closed during coarse progress; ignoring",
			"job_id", jobID,
		)
		return nil
	}
	if rs != nil && !rs.terminalQueued && rs.solReopens < rs.maxSOLReopens {
		// Allow reopens for a couple of minutes after stage start.
		if rs.stageStarted.IsZero() || time.Since(rs.stageStarted) < 2*time.Minute {
			p.orch.log.Warn("sol stream closed early; reopening",
				"job_id", jobID,
				"reopen", rs.solReopens+1,
			)
			go p.orch.reopenSOL(jobID)
			return nil
		}
	}
	msg := "transport error"
	if err != nil {
		msg = err.Error()
	}
	_ = p.orch.store.UpdateProgress(ctx, jobID, "TRANSPORT", nil, 0, msg)
	p.orch.appendJobLog(ctx, jobID, models.SOLMarker{
		SchemaVer: sol.SchemaVersion,
		Timestamp: time.Now().UTC(),
		Phase:     "TRANSPORT",
		State:     "ERROR",
		Detail:    msg,
	})
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

// applyDefaultCredentials fills empty username/password from orchestrator defaults
// (composition root wires SHOAL_BMC_*). Does not override credential_ref-only starts.
func (o *Orchestrator) applyDefaultCredentials(req *models.StartJobRequest) {
	if strings.TrimSpace(req.CredentialRef) != "" {
		return
	}
	if strings.TrimSpace(req.BMCUsername) == "" && o.defaultBMCUser != "" {
		req.BMCUsername = o.defaultBMCUser
	}
	if strings.TrimSpace(req.BMCPassword) == "" && o.defaultBMCPass != "" {
		req.BMCPassword = o.defaultBMCPass
	}
}

// resolveDeviceID remaps req.DeviceID via NetBox when a resolver is configured.
// Failures are logged and non-fatal so spike jobs without NetBox rows still start.
func (o *Orchestrator) resolveDeviceID(ctx context.Context, req *models.StartJobRequest) error {
	if o.resolver == nil {
		return nil
	}
	key := strings.TrimSpace(req.DeviceID)
	if key == "" {
		return nil
	}
	resolved, err := o.resolver.ResolveDeviceID(ctx, key)
	if err != nil {
		o.log.Warn("netbox device resolve failed; using device_id as-is",
			"device_id", key, "err", err.Error())
		return nil
	}
	resolved = strings.TrimSpace(resolved)
	if resolved == "" || resolved == key {
		return nil
	}
	o.log.Info("resolved device_id via NetBox",
		"from", key, "to", resolved)
	req.DeviceID = resolved
	return nil
}

// appendJobLog best-effort writes a SHOAL| line to telemetry job_log.
func (o *Orchestrator) appendJobLog(ctx context.Context, jobID string, m models.SOLMarker) {
	if o.telemetry == nil {
		return
	}
	line := sol.FormatMarkerLine(m)
	ts := m.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	if err := o.telemetry.WriteJobLog(ctx, jobID, ts, line); err != nil {
		o.log.Warn("job_log write failed", "job_id", jobID, "err", err.Error())
	}
}

var _ jobport.JobProgress = (*progressAdapter)(nil)
