package telemetry

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/validate"
)

// Postgres implements Store on the lab telemetry DB (events / sensor_readings / job_log).
type Postgres struct {
	db *sql.DB
}

// NewPostgres wraps an open, migrated *sql.DB.
func NewPostgres(db *sql.DB) *Postgres {
	return &Postgres{db: db}
}

// WriteEvent inserts one events row. Assigns ID when empty. Does not persist Raw.
func (p *Postgres) WriteEvent(ctx context.Context, e models.NormalizedEvent) error {
	if err := validate.NormalizedEvent(e); err != nil {
		return err
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	} else {
		e.Timestamp = e.Timestamp.UTC()
	}
	if e.ID == "" {
		e.ID = newEventID()
	}
	_, err := p.db.ExecContext(ctx, `
INSERT INTO events (id, device_id, ts, type, severity, component, message, raw_ref)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		e.ID, e.DeviceID, e.Timestamp,
		nullStr(e.EventType), nullStr(e.Severity), nullStr(e.Component),
		e.Message, nil,
	)
	if err != nil {
		return fmt.Errorf("telemetry: write event: %w", err)
	}
	return nil
}

// WriteSensor inserts one sensor_readings row.
func (p *Postgres) WriteSensor(ctx context.Context, r SensorReading) error {
	if r.DeviceID == "" {
		return fmt.Errorf("telemetry: sensor device_id required")
	}
	if r.Sensor == "" {
		return fmt.Errorf("telemetry: sensor name required")
	}
	if r.TS.IsZero() {
		r.TS = time.Now().UTC()
	} else {
		r.TS = r.TS.UTC()
	}
	_, err := p.db.ExecContext(ctx, `
INSERT INTO sensor_readings (device_id, ts, sensor, value, unit)
VALUES ($1,$2,$3,$4,$5)`,
		r.DeviceID, r.TS, r.Sensor, r.Value, nullStr(r.Unit),
	)
	if err != nil {
		return fmt.Errorf("telemetry: write sensor: %w", err)
	}
	return nil
}

// WriteJobLog inserts one job_log row.
func (p *Postgres) WriteJobLog(ctx context.Context, jobID string, ts time.Time, line string) error {
	if jobID == "" {
		return fmt.Errorf("telemetry: job_id required")
	}
	if line == "" {
		return fmt.Errorf("telemetry: log line required")
	}
	if ts.IsZero() {
		ts = time.Now().UTC()
	} else {
		ts = ts.UTC()
	}
	_, err := p.db.ExecContext(ctx, `
INSERT INTO job_log (job_id, ts, line) VALUES ($1,$2,$3)`,
		jobID, ts, line,
	)
	if err != nil {
		return fmt.Errorf("telemetry: write job log: %w", err)
	}
	return nil
}

// ListEvents returns newest-first events for a device since the given time.
func (p *Postgres) ListEvents(ctx context.Context, deviceID string, since time.Time, limit int) ([]models.NormalizedEvent, error) {
	if deviceID == "" {
		return nil, fmt.Errorf("telemetry: device_id required")
	}
	if limit <= 0 {
		limit = 100
	}
	var (
		rows *sql.Rows
		err  error
	)
	if since.IsZero() {
		rows, err = p.db.QueryContext(ctx, `
SELECT id, device_id, ts, type, severity, component, message
FROM events WHERE device_id = $1
ORDER BY ts DESC LIMIT $2`, deviceID, limit)
	} else {
		rows, err = p.db.QueryContext(ctx, `
SELECT id, device_id, ts, type, severity, component, message
FROM events WHERE device_id = $1 AND ts >= $2
ORDER BY ts DESC LIMIT $3`, deviceID, since.UTC(), limit)
	}
	if err != nil {
		return nil, fmt.Errorf("telemetry: list events: %w", err)
	}
	defer rows.Close()

	var out []models.NormalizedEvent
	for rows.Next() {
		var e models.NormalizedEvent
		var typ, sev, comp sql.NullString
		if err := rows.Scan(&e.ID, &e.DeviceID, &e.Timestamp, &typ, &sev, &comp, &e.Message); err != nil {
			return nil, fmt.Errorf("telemetry: scan event: %w", err)
		}
		e.EventType = typ.String
		e.Severity = sev.String
		e.Component = comp.String
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListSensors returns newest-first sensor rows for a device.
func (p *Postgres) ListSensors(ctx context.Context, deviceID string, since time.Time, limit int) ([]SensorReading, error) {
	if deviceID == "" {
		return nil, fmt.Errorf("telemetry: device_id required")
	}
	if limit <= 0 {
		limit = 100
	}
	var (
		rows *sql.Rows
		err  error
	)
	if since.IsZero() {
		rows, err = p.db.QueryContext(ctx, `
SELECT device_id, ts, sensor, value, unit
FROM sensor_readings WHERE device_id = $1
ORDER BY ts DESC LIMIT $2`, deviceID, limit)
	} else {
		rows, err = p.db.QueryContext(ctx, `
SELECT device_id, ts, sensor, value, unit
FROM sensor_readings WHERE device_id = $1 AND ts >= $2
ORDER BY ts DESC LIMIT $3`, deviceID, since.UTC(), limit)
	}
	if err != nil {
		return nil, fmt.Errorf("telemetry: list sensors: %w", err)
	}
	defer rows.Close()

	var out []SensorReading
	for rows.Next() {
		var r SensorReading
		var unit sql.NullString
		var val sql.NullFloat64
		if err := rows.Scan(&r.DeviceID, &r.TS, &r.Sensor, &val, &unit); err != nil {
			return nil, fmt.Errorf("telemetry: scan sensor: %w", err)
		}
		if val.Valid {
			r.Value = val.Float64
		}
		r.Unit = unit.String
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListJobLog returns oldest-first job_log lines for a job since the given time.
func (p *Postgres) ListJobLog(ctx context.Context, jobID string, since time.Time, limit int) ([]JobLogLine, error) {
	if jobID == "" {
		return nil, fmt.Errorf("telemetry: job_id required")
	}
	if limit <= 0 {
		limit = 100
	}
	var (
		rows *sql.Rows
		err  error
	)
	if since.IsZero() {
		rows, err = p.db.QueryContext(ctx, `
SELECT job_id, ts, line
FROM job_log WHERE job_id = $1
ORDER BY ts ASC LIMIT $2`, jobID, limit)
	} else {
		rows, err = p.db.QueryContext(ctx, `
SELECT job_id, ts, line
FROM job_log WHERE job_id = $1 AND ts >= $2
ORDER BY ts ASC LIMIT $3`, jobID, since.UTC(), limit)
	}
	if err != nil {
		return nil, fmt.Errorf("telemetry: list job log: %w", err)
	}
	defer rows.Close()

	var out []JobLogLine
	for rows.Next() {
		var l JobLogLine
		if err := rows.Scan(&l.JobID, &l.TS, &l.Line); err != nil {
			return nil, fmt.Errorf("telemetry: scan job log: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func newEventID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("evt-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

var _ Store = (*Postgres)(nil)
