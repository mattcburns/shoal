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
	"github.com/mattcburns/shoal/internal/observe/poll"
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
	Telemetry telemetry.Store
	Watches   WatchState
	// Optional one-shot poller for live refresh during status.
	Poller *poll.Poller
}

// New constructs an Observe service.
func New(log *slog.Logger, jobs jobport.JobQuery, store telemetry.Store, watches WatchState) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{Log: log, Jobs: jobs, Telemetry: store, Watches: watches}
}

// Status aggregates job + telemetry + optional watch flag for a device.
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
		st.ActiveJobID = job.ID
		st.Phase = job.Phase
		st.Percent = job.Percent
		if job.UpdatedAt != nil {
			st.UpdatedAt = job.UpdatedAt.UTC()
		}
		if job.Error != "" && st.LastEvent == "" {
			st.LastEvent = job.Error
		}
	}

	if s.Telemetry != nil {
		evs, err := s.Telemetry.ListEvents(ctx, deviceID, time.Time{}, 1)
		if err != nil {
			s.Log.Warn("list events for status", "device_id", deviceID, "err", err.Error())
		} else if len(evs) > 0 {
			st.LastEvent = formatEvent(evs[0])
			if evs[0].Timestamp.After(st.UpdatedAt) {
				st.UpdatedAt = evs[0].Timestamp
			}
		}
	}

	if s.Watches != nil && s.Watches.HasWatch(deviceID) {
		// Encode watch activity in last_event suffix only if empty phase context.
		if st.Phase == "" && st.ActiveJobID != "" {
			st.Phase = "WATCHING"
		}
	}

	return st, nil
}

// StatusWithPower optionally fills PowerState via Redfish GetSystem.
func (s *Service) StatusWithPower(ctx context.Context, deviceID string, bmc redfish.BMC, systemID string) (models.DeviceStatus, error) {
	st, err := s.Status(ctx, deviceID)
	if err != nil {
		return st, err
	}
	if bmc == nil {
		return st, nil
	}
	sys, err := bmc.GetSystem(ctx, systemID)
	if err != nil {
		s.Log.Warn("power state", "device_id", deviceID, "err", err.Error())
		return st, nil
	}
	st.PowerState = sys.PowerState
	return st, nil
}

// ListEvents returns recent telemetry events for a device.
func (s *Service) ListEvents(ctx context.Context, deviceID string, since time.Time, limit int) ([]models.NormalizedEvent, error) {
	if s.Telemetry == nil {
		return nil, fmt.Errorf("observe: telemetry store not configured")
	}
	if deviceID == "" {
		return nil, fmt.Errorf("observe: device_id required")
	}
	return s.Telemetry.ListEvents(ctx, deviceID, since, limit)
}

func (s *Service) latestJob(ctx context.Context, deviceID string) (models.ProvisioningJob, bool) {
	if s.Jobs == nil {
		return models.ProvisioningJob{}, false
	}
	// Prefer active provisioning; else most recently updated job for device.
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
			// Prefer PROVISIONING always when present.
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

// Ensure WatchService satisfies WatchState.
var _ WatchState = (*sol.WatchService)(nil)
