// Package jobstore is pure jobs-table persistence (CRUD / progress / transition).
// No Redfish, no Observe imports, no cleanup side effects.
package jobstore

import (
	"context"
	"errors"

	"github.com/mattcburns/shoal/internal/common/models"
)

// ErrNotFound is returned when a job id is unknown.
var ErrNotFound = errors.New("jobstore: job not found")

// Store is pure durable job repository.
type Store interface {
	Insert(ctx context.Context, job models.ProvisioningJob) error
	Get(ctx context.Context, id string) (models.ProvisioningJob, error)
	ListByState(ctx context.Context, state models.LifecycleState) ([]models.ProvisioningJob, error)
	// ListByDevice returns jobs for deviceID, newest-updated first, capped at limit
	// (<=0 uses the caller's default). state == "" means no state filter.
	ListByDevice(ctx context.Context, deviceID string, state models.LifecycleState, limit int) ([]models.ProvisioningJob, error)
	UpdateProgress(ctx context.Context, jobID string, phase string, percent *int, seq int, errSoft string) error
	// UpdateRuntime persists BMC runtime coordinates for cancel/orphan cleanup.
	UpdateRuntime(ctx context.Context, jobID string, systemID, solSessionID, credentialRef string) error
	// UpdateStages persists multi-stage runner fields (current_stage, strategy, stages list).
	UpdateStages(ctx context.Context, jobID string, currentStage, installStrategy string, stages []models.JobStage) error
	Transition(ctx context.Context, jobID string, to models.LifecycleState, errMsg string) error
}
