package jobstore

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
)

// Memory is an in-process Store for unit tests and DSN-less lab spikes.
type Memory struct {
	mu   sync.RWMutex
	jobs map[string]models.ProvisioningJob
}

// NewMemory returns an empty memory job store.
func NewMemory() *Memory {
	return &Memory{jobs: make(map[string]models.ProvisioningJob)}
}

// Insert stores a new job. Fails if id already exists.
func (m *Memory) Insert(_ context.Context, job models.ProvisioningJob) error {
	if job.ID == "" {
		return fmt.Errorf("jobstore: empty job id")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.jobs[job.ID]; ok {
		return fmt.Errorf("jobstore: job %s already exists", job.ID)
	}
	now := time.Now().UTC()
	if job.UpdatedAt == nil {
		job.UpdatedAt = &now
	}
	m.jobs[job.ID] = cloneJob(job)
	return nil
}

// Get returns a job by id.
func (m *Memory) Get(_ context.Context, id string) (models.ProvisioningJob, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	j, ok := m.jobs[id]
	if !ok {
		return models.ProvisioningJob{}, ErrNotFound
	}
	return cloneJob(j), nil
}

// ListByState returns all jobs in state.
func (m *Memory) ListByState(_ context.Context, state models.LifecycleState) ([]models.ProvisioningJob, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []models.ProvisioningJob
	for _, j := range m.jobs {
		if j.State == state {
			out = append(out, cloneJob(j))
		}
	}
	return out, nil
}

// UpdateProgress updates phase/percent/seq/error without changing lifecycle state.
func (m *Memory) UpdateProgress(_ context.Context, jobID string, phase string, percent *int, seq int, errSoft string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[jobID]
	if !ok {
		return ErrNotFound
	}
	if phase != "" {
		j.Phase = phase
	}
	if percent != nil {
		p := *percent
		j.Percent = &p
	}
	if seq > j.LastMarkerSeq {
		j.LastMarkerSeq = seq
	}
	if errSoft != "" {
		j.Error = errSoft
	}
	now := time.Now().UTC()
	j.UpdatedAt = &now
	m.jobs[jobID] = j
	return nil
}

// UpdateRuntime persists system_id, sol_session_id, and credential_ref.
func (m *Memory) UpdateRuntime(_ context.Context, jobID string, systemID, solSessionID, credentialRef string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[jobID]
	if !ok {
		return ErrNotFound
	}
	if systemID != "" {
		j.SystemID = systemID
	}
	if solSessionID != "" {
		j.SOLSessionID = solSessionID
	}
	if credentialRef != "" {
		j.CredentialRef = credentialRef
	}
	now := time.Now().UTC()
	j.UpdatedAt = &now
	m.jobs[jobID] = j
	return nil
}

// Transition sets lifecycle state and optional error message.
func (m *Memory) Transition(_ context.Context, jobID string, to models.LifecycleState, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[jobID]
	if !ok {
		return ErrNotFound
	}
	j.State = to
	if errMsg != "" {
		j.Error = errMsg
	}
	now := time.Now().UTC()
	j.UpdatedAt = &now
	m.jobs[jobID] = j
	return nil
}

func cloneJob(j models.ProvisioningJob) models.ProvisioningJob {
	out := j
	if j.Percent != nil {
		p := *j.Percent
		out.Percent = &p
	}
	if j.StartedAt != nil {
		t := *j.StartedAt
		out.StartedAt = &t
	}
	if j.UpdatedAt != nil {
		t := *j.UpdatedAt
		out.UpdatedAt = &t
	}
	return out
}
