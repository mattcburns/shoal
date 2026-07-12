package jobstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
)

// Postgres is a durable Store backed by the jobs table.
type Postgres struct {
	db *sql.DB
}

// NewPostgres wraps an open *sql.DB (already migrated).
func NewPostgres(db *sql.DB) *Postgres {
	return &Postgres{db: db}
}

// Insert inserts a job row.
func (p *Postgres) Insert(ctx context.Context, job models.ProvisioningJob) error {
	if job.ID == "" {
		return fmt.Errorf("jobstore: empty job id")
	}
	now := time.Now().UTC()
	if job.UpdatedAt == nil {
		job.UpdatedAt = &now
	}
	_, err := p.db.ExecContext(ctx, `
INSERT INTO jobs (
  id, device_id, profile_ref, state, attempt, phase, percent, last_marker_seq,
  started_at, updated_at, error, sol_session_id, iso_url, bmc_endpoint
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		job.ID, job.DeviceID, job.ProfileRef, string(job.State), job.Attempt,
		nullString(job.Phase), nullInt(job.Percent), job.LastMarkerSeq,
		job.StartedAt, job.UpdatedAt, nullString(job.Error), nullString(job.SOLSessionID),
		nullString(job.ISOURL), nullString(job.BMCEndpoint),
	)
	if err != nil {
		return fmt.Errorf("jobstore: insert: %w", err)
	}
	return nil
}

// Get loads a job by id.
func (p *Postgres) Get(ctx context.Context, id string) (models.ProvisioningJob, error) {
	row := p.db.QueryRowContext(ctx, `
SELECT id, device_id, profile_ref, state, attempt, phase, percent, last_marker_seq,
       started_at, updated_at, error, sol_session_id, iso_url, bmc_endpoint
FROM jobs WHERE id = $1`, id)
	j, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return models.ProvisioningJob{}, ErrNotFound
	}
	if err != nil {
		return models.ProvisioningJob{}, fmt.Errorf("jobstore: get: %w", err)
	}
	return j, nil
}

// ListByState returns jobs in the given lifecycle state.
func (p *Postgres) ListByState(ctx context.Context, state models.LifecycleState) ([]models.ProvisioningJob, error) {
	rows, err := p.db.QueryContext(ctx, `
SELECT id, device_id, profile_ref, state, attempt, phase, percent, last_marker_seq,
       started_at, updated_at, error, sol_session_id, iso_url, bmc_endpoint
FROM jobs WHERE state = $1`, string(state))
	if err != nil {
		return nil, fmt.Errorf("jobstore: list: %w", err)
	}
	defer rows.Close()
	var out []models.ProvisioningJob
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// UpdateProgress updates progress fields only.
func (p *Postgres) UpdateProgress(ctx context.Context, jobID string, phase string, percent *int, seq int, errSoft string) error {
	now := time.Now().UTC()
	res, err := p.db.ExecContext(ctx, `
UPDATE jobs SET
  phase = CASE WHEN $2 <> '' THEN $2 ELSE phase END,
  percent = COALESCE($3, percent),
  last_marker_seq = GREATEST(last_marker_seq, $4),
  error = CASE WHEN $5 <> '' THEN $5 ELSE error END,
  updated_at = $6
WHERE id = $1`, jobID, phase, nullInt(percent), seq, errSoft, now)
	if err != nil {
		return fmt.Errorf("jobstore: update progress: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Transition sets lifecycle state.
func (p *Postgres) Transition(ctx context.Context, jobID string, to models.LifecycleState, errMsg string) error {
	now := time.Now().UTC()
	res, err := p.db.ExecContext(ctx, `
UPDATE jobs SET
  state = $2,
  error = CASE WHEN $3 <> '' THEN $3 ELSE error END,
  updated_at = $4
WHERE id = $1`, jobID, string(to), errMsg, now)
	if err != nil {
		return fmt.Errorf("jobstore: transition: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanJob(row scannable) (models.ProvisioningJob, error) {
	var j models.ProvisioningJob
	var state string
	var phase, errMsg, solSess, iso, bmc sql.NullString
	var percent sql.NullInt64
	var started, updated sql.NullTime
	err := row.Scan(
		&j.ID, &j.DeviceID, &j.ProfileRef, &state, &j.Attempt,
		&phase, &percent, &j.LastMarkerSeq,
		&started, &updated, &errMsg, &solSess, &iso, &bmc,
	)
	if err != nil {
		return models.ProvisioningJob{}, err
	}
	j.State = models.LifecycleState(state)
	if phase.Valid {
		j.Phase = phase.String
	}
	if percent.Valid {
		p := int(percent.Int64)
		j.Percent = &p
	}
	if started.Valid {
		t := started.Time.UTC()
		j.StartedAt = &t
	}
	if updated.Valid {
		t := updated.Time.UTC()
		j.UpdatedAt = &t
	}
	if errMsg.Valid {
		j.Error = errMsg.String
	}
	if solSess.Valid {
		j.SOLSessionID = solSess.String
	}
	if iso.Valid {
		j.ISOURL = iso.String
	}
	if bmc.Valid {
		j.BMCEndpoint = bmc.String
	}
	return j, nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
