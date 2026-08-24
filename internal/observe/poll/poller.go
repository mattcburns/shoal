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

// Default idle vs watch-elevated intervals (serve overrides from SHOAL_POLL_*).
const (
	DefaultIdleInterval  = 5 * time.Minute
	DefaultWatchInterval = 30 * time.Second
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
	Log    *slog.Logger
	Store  telemetry.Store
	NewBMC redfish.Factory
	// Events is optional Core reconciler; when nil, deterministic normalizeSELEntry is used.
	Events   reconcile.Reconciler
	Watching WatchChecker

	IdleInterval  time.Duration
	WatchInterval time.Duration
	MaxConcurrent int
	SELMaxEntries int

	mu      sync.Mutex
	targets map[string]Target
	seenSEL map[string]map[string]struct{}
	sem     chan struct{}
}

// New constructs a Poller with defaults. newBMC nil → redfish.NewBMC.
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

// Result is the outcome of one PollOnce.
type Result struct {
	SELNew          int
	SensorsWritten  int
	FirmwareWritten int
	PowerState      string
}

// PollOnce runs a single SEL+sensor+firmware+power poll for one target (deduped SEL writes).
// Returns a non-nil error if Redfish fails or any normalize/write fails (counts
// still reflect successful writes). Empty SEL/sensors/firmware with nil error is valid.
func (p *Poller) PollOnce(ctx context.Context, t Target) (Result, error) {
	if p.Store == nil {
		return Result{}, fmt.Errorf("poll: telemetry store not configured")
	}
	if t.DeviceID == "" || t.BMC.BaseURL == "" {
		return Result{}, fmt.Errorf("poll: incomplete target")
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
		return Result{}, ctx.Err()
	}

	bmc, err := p.NewBMC(t.BMC)
	if err != nil {
		return Result{}, fmt.Errorf("poll: bmc: %w", err)
	}
	if err := bmc.Open(ctx); err != nil {
		return Result{}, fmt.Errorf("poll: open: %w", err)
	}
	defer func() { _ = bmc.Close(context.Background()) }()

	maxSEL := p.SELMaxEntries
	if maxSEL <= 0 {
		maxSEL = DefaultSELMaxEntries
	}
	entries, selErr := bmc.ListSEL(ctx, t.SystemID, redfish.SELOptions{MaxEntries: maxSEL})
	if selErr != nil {
		entries = nil
	}
	samples, sensErr := bmc.ListSensors(ctx, t.SystemID)
	if sensErr != nil {
		samples = nil
	}
	fwItems, fwErr := bmc.ListFirmware(ctx)
	if fwErr != nil {
		fwItems = nil
	}
	sys, sysErr := bmc.GetSystem(ctx, t.SystemID)

	p.mu.Lock()
	if p.seenSEL[t.DeviceID] == nil {
		p.seenSEL[t.DeviceID] = make(map[string]struct{})
	}
	seen := p.seenSEL[t.DeviceID]
	p.mu.Unlock()

	var failN int
	var firstFail error
	out := Result{}
	now := time.Now().UTC()

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
			failN++
			if firstFail == nil {
				firstFail = fmt.Errorf("normalize: %w", nerr)
			}
			p.Log.Warn("poll normalize sel", "device_id", t.DeviceID, "err", nerr.Error())
			continue
		}
		if err := p.Store.WriteEvent(ctx, ev); err != nil {
			failN++
			if firstFail == nil {
				firstFail = fmt.Errorf("write event: %w", err)
			}
			p.Log.Warn("poll write event", "device_id", t.DeviceID, "err", err.Error())
			continue
		}
		out.SELNew++
	}

	for _, s := range samples {
		name := s.Name
		if name == "" {
			name = s.Kind
		}
		if name == "" {
			failN++
			if firstFail == nil {
				firstFail = fmt.Errorf("sensor sample missing name")
			}
			continue
		}
		row := telemetry.SensorReading{
			DeviceID: t.DeviceID,
			TS:       now,
			Sensor:   name,
			Unit:     s.Units,
			Note:     s.Note,
		}
		if s.HasReading {
			row.Value = telemetry.SensorValue(s.Reading)
		}
		if err := p.Store.WriteSensor(ctx, row); err != nil {
			failN++
			if firstFail == nil {
				firstFail = fmt.Errorf("write sensor: %w", err)
			}
			p.Log.Warn("poll write sensor", "device_id", t.DeviceID, "err", err.Error())
			continue
		}
		out.SensorsWritten++
	}

	for _, fw := range fwItems {
		id := strings.TrimSpace(fw.ID)
		if id == "" {
			id = strings.TrimSpace(fw.Name)
		}
		if id == "" {
			failN++
			if firstFail == nil {
				firstFail = fmt.Errorf("firmware item missing id")
			}
			continue
		}
		row := telemetry.FirmwareComponent{
			DeviceID:     t.DeviceID,
			TS:           now,
			ID:           id,
			Name:         fw.Name,
			Version:      fw.Version,
			Manufacturer: fw.Manufacturer,
			SoftwareID:   fw.SoftwareID,
			Health:       fw.Health,
			Updateable:   fw.Updateable,
			ReleaseDate:  fw.ReleaseDate,
		}
		if err := p.Store.WriteFirmware(ctx, row); err != nil {
			failN++
			if firstFail == nil {
				firstFail = fmt.Errorf("write firmware: %w", err)
			}
			p.Log.Warn("poll write firmware", "device_id", t.DeviceID, "err", err.Error())
			continue
		}
		out.FirmwareWritten++
	}

	if sysErr == nil && strings.TrimSpace(sys.PowerState) != "" {
		out.PowerState = sys.PowerState
		if err := p.Store.WritePower(ctx, telemetry.PowerReading{
			DeviceID: t.DeviceID, TS: now, PowerState: sys.PowerState,
		}); err != nil {
			failN++
			if firstFail == nil {
				firstFail = fmt.Errorf("write power: %w", err)
			}
			p.Log.Warn("poll write power", "device_id", t.DeviceID, "err", err.Error())
		}
	}

	p.Log.Info("poll complete",
		"device_id", t.DeviceID,
		"sel_new", out.SELNew,
		"sensors", out.SensorsWritten,
		"firmware", out.FirmwareWritten,
		"power_state", out.PowerState,
		"failures", failN,
		"sel_seen", len(entries),
		"sensor_seen", len(samples),
		"firmware_seen", len(fwItems),
	)
	if selErr != nil {
		return out, fmt.Errorf("poll: list sel: %w", selErr)
	}
	if sensErr != nil {
		return out, fmt.Errorf("poll: list sensors: %w", sensErr)
	}
	if fwErr != nil {
		return out, fmt.Errorf("poll: list firmware: %w", fwErr)
	}
	if sysErr != nil {
		return out, fmt.Errorf("poll: power state: %w", sysErr)
	}
	if failN > 0 {
		return out, fmt.Errorf("poll: %d item failure(s) after sel_new=%d sensors=%d firmware=%d: %w",
			failN, out.SELNew, out.SensorsWritten, out.FirmwareWritten, firstFail)
	}
	return out, nil
}

func (p *Poller) normalizeSEL(ctx context.Context, deviceID string, e redfish.SELEntry) (models.NormalizedEvent, error) {
	if p.Events != nil {
		raw := map[string]any{
			"severity":    e.Severity,
			"entry_type":  e.EntryType,
			"sensor_type": e.SensorType,
			"log_service": e.LogService,
			"odata_id":    e.ODataID,
		}
		msg := e.Message
		if msg == "" {
			msg = e.ID
		}
		return p.Events.ReconcileEvent(ctx, models.RawEventInput{
			DeviceID:  deviceID,
			Source:    "sel",
			Timestamp: e.Created,
			Message:   msg,
			Raw:       raw,
		})
	}
	return normalizeSELEntry(deviceID, e), nil
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
			if _, err := p.PollOnce(ctx, t); err != nil && ctx.Err() == nil {
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
