// Package observe aggregates device status and owns poll/SOL surfaces.
package observe

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/mattcburns/shoal/internal/common/jobport"
	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/common/telemetry"
	"github.com/mattcburns/shoal/internal/observe/sol"
)

// WatchState reports active SOL ownership (implemented by sol.WatchService).
type WatchState interface {
	HasWatch(deviceID string) bool
}

// Service builds DeviceStatus and optional event listings for operators.
type Service struct {
	Log       *slog.Logger
	Jobs      jobport.JobQuery
	Telemetry telemetry.Store // nil → no events in status / ListEvents errors
	Watches   WatchState
}

// New constructs an Observe service.
func New(log *slog.Logger, jobs jobport.JobQuery, store telemetry.Store, watches WatchState) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{Log: log, Jobs: jobs, Telemetry: store, Watches: watches}
}

// Status aggregates job + telemetry for a device.
// ActiveJobID is set only when the latest job is PROVISIONING (truly active).
// Does not invent SOL phases (e.g. no synthetic "WATCHING").
func (s *Service) Status(ctx context.Context, deviceID string) (models.DeviceStatus, error) {
	if deviceID == "" {
		return models.DeviceStatus{}, fmt.Errorf("observe: device_id required")
	}
	st := models.DeviceStatus{
		DeviceID:  deviceID,
		UpdatedAt: time.Now().UTC(),
	}

	if job, ok := s.latestJob(ctx, deviceID); ok {
		st.LifecycleState = job.State
		st.Phase = job.Phase
		st.Percent = job.Percent
		if job.State == models.StateProvisioning {
			st.ActiveJobID = job.ID
		}
		if job.UpdatedAt != nil {
			st.UpdatedAt = job.UpdatedAt.UTC()
		}
		if job.Error != "" {
			st.LastEvent = job.Error
		}
	}

	if s.Telemetry != nil {
		if pw, err := s.Telemetry.LatestPower(ctx, deviceID); err == nil && pw.PowerState != "" {
			st.PowerState = pw.PowerState
			if pw.TS.After(st.UpdatedAt) {
				st.UpdatedAt = pw.TS
			}
		}
		evs, err := s.Telemetry.ListEvents(ctx, deviceID, time.Time{}, 1)
		if err != nil {
			return st, fmt.Errorf("observe: list events: %w", err)
		}
		if len(evs) > 0 {
			// Prefer telemetry message when present; keep job error if no events.
			st.LastEvent = formatEvent(evs[0])
			if evs[0].Timestamp.After(st.UpdatedAt) {
				st.UpdatedAt = evs[0].Timestamp
			}
		}
	}

	return st, nil
}

// StatusWithPower fills PowerState via Redfish GetSystem. BMC errors are returned
// (caller must not treat missing power as success when power was requested).
func (s *Service) StatusWithPower(ctx context.Context, deviceID string, bmc redfish.BMC, systemID string) (models.DeviceStatus, error) {
	st, err := s.Status(ctx, deviceID)
	if err != nil {
		return st, err
	}
	if bmc == nil {
		return st, fmt.Errorf("observe: bmc required for power state")
	}
	sys, err := bmc.GetSystem(ctx, systemID)
	if err != nil {
		return st, fmt.Errorf("observe: power state: %w", err)
	}
	st.PowerState = sys.PowerState
	return st, nil
}

// ListEvents returns recent telemetry events for a device.
func (s *Service) ListEvents(ctx context.Context, deviceID string, since time.Time, limit int) ([]models.NormalizedEvent, error) {
	if s.Telemetry == nil {
		return nil, fmt.Errorf("observe: telemetry store not configured (set SHOAL_TELEMETRY_DATABASE_URL)")
	}
	if deviceID == "" {
		return nil, fmt.Errorf("observe: device_id required")
	}
	return s.Telemetry.ListEvents(ctx, deviceID, since, limit)
}

// ListFirmware returns the latest firmware inventory snapshot for a device.
func (s *Service) ListFirmware(ctx context.Context, deviceID string, limit int) ([]telemetry.FirmwareComponent, error) {
	if s.Telemetry == nil {
		return nil, fmt.Errorf("observe: telemetry store not configured (set SHOAL_TELEMETRY_DATABASE_URL)")
	}
	if deviceID == "" {
		return nil, fmt.Errorf("observe: device_id required")
	}
	return s.Telemetry.ListFirmware(ctx, deviceID, limit)
}

// ListSensors returns recent sensor readings for a device.
func (s *Service) ListSensors(ctx context.Context, deviceID string, since time.Time, limit int) ([]telemetry.SensorReading, error) {
	if s.Telemetry == nil {
		return nil, fmt.Errorf("observe: telemetry store not configured (set SHOAL_TELEMETRY_DATABASE_URL)")
	}
	if deviceID == "" {
		return nil, fmt.Errorf("observe: device_id required")
	}
	return s.Telemetry.ListSensors(ctx, deviceID, since, limit)
}

// ListJobLog returns durable job_log lines for a job.
func (s *Service) ListJobLog(ctx context.Context, jobID string, since time.Time, limit int) ([]telemetry.JobLogLine, error) {
	if s.Telemetry == nil {
		return nil, fmt.Errorf("observe: telemetry store not configured (set SHOAL_TELEMETRY_DATABASE_URL)")
	}
	if jobID == "" {
		return nil, fmt.Errorf("observe: job_id required")
	}
	return s.Telemetry.ListJobLog(ctx, jobID, since, limit)
}

// Watching reports whether a SOL watch is active (for operators; not embedded in Phase).
func (s *Service) Watching(deviceID string) bool {
	return s.Watches != nil && s.Watches.HasWatch(deviceID)
}

func (s *Service) latestJob(ctx context.Context, deviceID string) (models.ProvisioningJob, bool) {
	if s.Jobs == nil {
		return models.ProvisioningJob{}, false
	}
	states := []models.LifecycleState{
		models.StateProvisioning,
		models.StateProvisioned,
		models.StateFailed,
		models.StateReady,
		models.StateDiscovered,
	}
	var best models.ProvisioningJob
	var found bool
	for _, st := range states {
		list, err := s.Jobs.ListByState(ctx, st)
		if err != nil {
			s.Log.Warn("list jobs by state", "state", string(st), "err", err.Error())
			continue
		}
		for _, j := range list {
			if j.DeviceID != deviceID {
				continue
			}
			if !found {
				best, found = j, true
				continue
			}
			if best.State != models.StateProvisioning && j.State == models.StateProvisioning {
				best = j
				continue
			}
			if best.State == models.StateProvisioning && j.State != models.StateProvisioning {
				continue
			}
			bt, jt := time.Time{}, time.Time{}
			if best.UpdatedAt != nil {
				bt = *best.UpdatedAt
			}
			if j.UpdatedAt != nil {
				jt = *j.UpdatedAt
			}
			if jt.After(bt) {
				best = j
			}
		}
		if found && best.State == models.StateProvisioning {
			return best, true
		}
	}
	return best, found
}

func formatEvent(e models.NormalizedEvent) string {
	if e.Severity != "" && e.Message != "" {
		return e.Severity + ": " + e.Message
	}
	return e.Message
}

var _ WatchState = (*sol.WatchService)(nil)
