// Package poll implements SEL/sensor polling for Observe (Phase 4).
package poll

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/common/telemetry"
	"github.com/mattcburns/shoal/internal/core/reconcile"
)

// Default idle vs watch-elevated intervals.
const (
	DefaultIdleInterval  = 60 * time.Second
	DefaultWatchInterval = 15 * time.Second
	DefaultMaxConcurrent = 1
	DefaultSELMaxEntries = 100
)

// Target is one device BMC to poll.
type Target struct {
	DeviceID string
	BMC      redfish.Config
	SystemID string // optional; empty = first/single system
}

// WatchChecker reports whether a device has an active SOL watch (elevated rate).
type WatchChecker interface {
	HasWatch(deviceID string) bool
}

// Poller reads SEL/sensors via Redfish and writes telemetry.
type Poller struct {
	Log      *slog.Logger
	Store    telemetry.Store
	NewBMC   redfish.Factory
	Events   reconcile.Reconciler // optional; deterministic path if nil
	Watching WatchChecker         // optional

	IdleInterval  time.Duration
	WatchInterval time.Duration
	MaxConcurrent int
	SELMaxEntries int

	mu      sync.Mutex
	targets map[string]Target // deviceID -> target
	seenSEL map[string]map[string]struct{}
	sem     chan struct{}
}

// New constructs a Poller with defaults.
func New(log *slog.Logger, store telemetry.Store, newBMC redfish.Factory) *Poller {
	if log == nil {
		log = slog.Default()
	}
	if newBMC == nil {
		newBMC = redfish.NewBMC
	}
	p := &Poller{
		Log:           log,
		Store:         store,
		NewBMC:        newBMC,
		IdleInterval:  DefaultIdleInterval,
		WatchInterval: DefaultWatchInterval,
		MaxConcurrent: DefaultMaxConcurrent,
		SELMaxEntries: DefaultSELMaxEntries,
		targets:       make(map[string]Target),
		seenSEL:       make(map[string]map[string]struct{}),
	}
	p.sem = make(chan struct{}, p.MaxConcurrent)
	return p
}

// SetTarget adds or replaces a poll target.
func (p *Poller) SetTarget(t Target) error {
	if t.DeviceID == "" {
		return fmt.Errorf("poll: device_id required")
	}
	if t.BMC.BaseURL == "" {
		return fmt.Errorf("poll: bmc base url required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.targets[t.DeviceID] = t
	if p.seenSEL[t.DeviceID] == nil {
		p.seenSEL[t.DeviceID] = make(map[string]struct{})
	}
	return nil
}

// RemoveTarget drops a device from the poll set.
func (p *Poller) RemoveTarget(deviceID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.targets, deviceID)
}

// Targets returns a snapshot of configured targets.
func (p *Poller) Targets() []Target {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Target, 0, len(p.targets))
	for _, t := range p.targets {
		out = append(out, t)
	}
	return out
}

// PollOnce runs a single SEL+sensor poll for one target (deduped SEL writes).
func (p *Poller) PollOnce(ctx context.Context, t Target) (selWritten, sensorsWritten int, err error) {
	if p.Store == nil {
		return 0, 0, fmt.Errorf("poll: telemetry store not configured")
	}
	if t.DeviceID == "" || t.BMC.BaseURL == "" {
		return 0, 0, fmt.Errorf("poll: incomplete target")
	}
	if p.MaxConcurrent <= 0 {
		p.MaxConcurrent = DefaultMaxConcurrent
	}
	if p.sem == nil {
		p.sem = make(chan struct{}, p.MaxConcurrent)
	}

	select {
	case p.sem <- struct{}{}:
		defer func() { <-p.sem }()
	case <-ctx.Done():
		return 0, 0, ctx.Err()
	}

	bmc, err := p.NewBMC(t.BMC)
	if err != nil {
		return 0, 0, fmt.Errorf("poll: bmc: %w", err)
	}
	if err := bmc.Open(ctx); err != nil {
		return 0, 0, fmt.Errorf("poll: open: %w", err)
	}
	defer func() { _ = bmc.Close(context.Background()) }()

	maxSEL := p.SELMaxEntries
	if maxSEL <= 0 {
		maxSEL = DefaultSELMaxEntries
	}
	entries, err := bmc.ListSEL(ctx, t.SystemID, redfish.SELOptions{MaxEntries: maxSEL})
	if err != nil {
		return 0, 0, fmt.Errorf("poll: list sel: %w", err)
	}
	samples, err := bmc.ListSensors(ctx, t.SystemID)
	if err != nil {
		return 0, 0, fmt.Errorf("poll: list sensors: %w", err)
	}

	p.mu.Lock()
	if p.seenSEL[t.DeviceID] == nil {
		p.seenSEL[t.DeviceID] = make(map[string]struct{})
	}
	seen := p.seenSEL[t.DeviceID]
	p.mu.Unlock()

	for _, e := range entries {
		key := e.ODataID
		if key == "" {
			key = e.ID + "|" + e.Message + "|" + e.Created.UTC().Format(time.RFC3339Nano)
		}
		p.mu.Lock()
		_, already := seen[key]
		if !already {
			seen[key] = struct{}{}
		}
		p.mu.Unlock()
		if already {
			continue
		}
		ev, nerr := p.normalizeSEL(ctx, t.DeviceID, e)
		if nerr != nil {
			p.Log.Warn("poll normalize sel", "device_id", t.DeviceID, "err", nerr.Error())
			continue
		}
		if err := p.Store.WriteEvent(ctx, ev); err != nil {
			p.Log.Warn("poll write event", "device_id", t.DeviceID, "err", err.Error())
			continue
		}
		selWritten++
	}

	now := time.Now().UTC()
	for _, s := range samples {
		name := s.Name
		if name == "" {
			name = s.Kind
		}
		if name == "" {
			continue
		}
		if err := p.Store.WriteSensor(ctx, telemetry.SensorReading{
			DeviceID: t.DeviceID,
			TS:       now,
			Sensor:   name,
			Value:    s.Reading,
			Unit:     s.Units,
		}); err != nil {
			p.Log.Warn("poll write sensor", "device_id", t.DeviceID, "err", err.Error())
			continue
		}
		sensorsWritten++
	}
	if selWritten > 0 || sensorsWritten > 0 {
		p.Log.Info("poll complete",
			"device_id", t.DeviceID,
			"sel_new", selWritten,
			"sensors", sensorsWritten,
		)
	}
	return selWritten, sensorsWritten, nil
}

func (p *Poller) normalizeSEL(ctx context.Context, deviceID string, e redfish.SELEntry) (models.NormalizedEvent, error) {
	msg := e.Message
	if msg == "" {
		msg = e.ID
	}
	raw := map[string]any{
		"severity":    e.Severity,
		"entry_type":  e.EntryType,
		"sensor_type": e.SensorType,
		"log_service": e.LogService,
		"odata_id":    e.ODataID,
	}
	in := models.RawEventInput{
		DeviceID:  deviceID,
		Source:    "sel",
		Timestamp: e.Created,
		Message:   msg,
		Raw:       raw,
	}
	if p.Events != nil {
		return p.Events.ReconcileEvent(ctx, in)
	}
	// Inline deterministic fallback (no Core wired).
	sev := "info"
	switch strings.ToLower(e.Severity) {
	case "critical":
		sev = "critical"
	case "warning":
		sev = "warning"
	case "error":
		sev = "error"
	case "ok", "":
		sev = "info"
	default:
		sev = strings.ToLower(e.Severity)
		if sev == "" {
			sev = "info"
		}
	}
	ts := e.Created
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	return models.NormalizedEvent{
		DeviceID:  deviceID,
		EventType: "sel",
		Severity:  sev,
		Component: e.SensorType,
		Message:   msg,
		Timestamp: ts,
	}, nil
}

// Run loops until ctx is cancelled. Polls all targets; uses WatchInterval when
// Watching.HasWatch(device) is true.
func (p *Poller) Run(ctx context.Context) {
	if p.IdleInterval <= 0 {
		p.IdleInterval = DefaultIdleInterval
	}
	if p.WatchInterval <= 0 {
		p.WatchInterval = DefaultWatchInterval
	}
	// Tick frequently enough to honor watch elevated rate; skip devices not due yet.
	ticker := time.NewTicker(p.WatchInterval)
	defer ticker.Stop()
	last := make(map[string]time.Time)

	pollAll := func() {
		for _, t := range p.Targets() {
			interval := p.IdleInterval
			if p.Watching != nil && p.Watching.HasWatch(t.DeviceID) {
				interval = p.WatchInterval
			}
			if t0, ok := last[t.DeviceID]; ok && time.Since(t0) < interval {
				continue
			}
			if _, _, err := p.PollOnce(ctx, t); err != nil && ctx.Err() == nil {
				p.Log.Warn("poll once failed", "device_id", t.DeviceID, "err", err.Error())
			}
			last[t.DeviceID] = time.Now()
		}
	}

	pollAll()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pollAll()
		}
	}
}

// IntervalFor returns the poll interval that would apply for deviceID.
func (p *Poller) IntervalFor(deviceID string) time.Duration {
	if p.Watching != nil && p.Watching.HasWatch(deviceID) {
		if p.WatchInterval > 0 {
			return p.WatchInterval
		}
		return DefaultWatchInterval
	}
	if p.IdleInterval > 0 {
		return p.IdleInterval
	}
	return DefaultIdleInterval
}
