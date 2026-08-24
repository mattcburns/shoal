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
INSERT INTO sensor_readings (device_id, ts, sensor, value, unit, note)
VALUES ($1,$2,$3,$4,$5,$6)`,
		r.DeviceID, r.TS, r.Sensor, nullFloat(r.Value), nullStr(r.Unit), nullStr(r.Note),
	)
	if err != nil {
		return fmt.Errorf("telemetry: write sensor: %w", err)
	}
	return nil
}

// WritePower upserts the latest power state for a device.
func (p *Postgres) WritePower(ctx context.Context, r PowerReading) error {
	if r.DeviceID == "" {
		return fmt.Errorf("telemetry: power device_id required")
	}
	if r.PowerState == "" {
		return fmt.Errorf("telemetry: power_state required")
	}
	if r.TS.IsZero() {
		r.TS = time.Now().UTC()
	} else {
		r.TS = r.TS.UTC()
	}
	_, err := p.db.ExecContext(ctx, `
INSERT INTO device_power (device_id, ts, power_state)
VALUES ($1,$2,$3)
ON CONFLICT (device_id) DO UPDATE SET ts = EXCLUDED.ts, power_state = EXCLUDED.power_state`,
		r.DeviceID, r.TS, r.PowerState)
	if err != nil {
		return fmt.Errorf("telemetry: write power: %w", err)
	}
	return nil
}

// WriteFirmware inserts one firmware_inventory row.
func (p *Postgres) WriteFirmware(ctx context.Context, c FirmwareComponent) error {
	if c.DeviceID == "" {
		return fmt.Errorf("telemetry: firmware device_id required")
	}
	if c.ID == "" && c.Name == "" {
		return fmt.Errorf("telemetry: firmware id or name required")
	}
	if c.TS.IsZero() {
		c.TS = time.Now().UTC()
	} else {
		c.TS = c.TS.UTC()
	}
	_, err := p.db.ExecContext(ctx, `
INSERT INTO firmware_inventory (device_id, ts, component_id, name, version, manufacturer, software_id, health, updateable, release_date)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		c.DeviceID, c.TS, c.ID, nullStr(c.Name), nullStr(c.Version), nullStr(c.Manufacturer),
		nullStr(c.SoftwareID), nullStr(c.Health), c.Updateable, nullStr(c.ReleaseDate))
	if err != nil {
		return fmt.Errorf("telemetry: write firmware: %w", err)
	}
	return nil
}

// LatestPower returns the last polled power state (zero value if none).
func (p *Postgres) LatestPower(ctx context.Context, deviceID string) (PowerReading, error) {
	if deviceID == "" {
		return PowerReading{}, fmt.Errorf("telemetry: device_id required")
	}
	var r PowerReading
	err := p.db.QueryRowContext(ctx, `
SELECT device_id, ts, power_state FROM device_power WHERE device_id = $1`, deviceID).
		Scan(&r.DeviceID, &r.TS, &r.PowerState)
	if err == sql.ErrNoRows {
		return PowerReading{}, nil
	}
	if err != nil {
		return PowerReading{}, fmt.Errorf("telemetry: latest power: %w", err)
	}
	return r, nil
}

// ListFirmware returns the latest firmware snapshot for a device.
func (p *Postgres) ListFirmware(ctx context.Context, deviceID string, limit int) ([]FirmwareComponent, error) {
	if deviceID == "" {
		return nil, fmt.Errorf("telemetry: device_id required")
	}
	if limit <= 0 {
		limit = 200
	}
	rows, err := p.db.QueryContext(ctx, `
SELECT device_id, ts, component_id, name, version, manufacturer, software_id, health, updateable, release_date
FROM firmware_inventory
WHERE device_id = $1
  AND ts = (SELECT MAX(ts) FROM firmware_inventory WHERE device_id = $1)
ORDER BY name, component_id
LIMIT $2`, deviceID, limit)
	if err != nil {
		return nil, fmt.Errorf("telemetry: list firmware: %w", err)
	}
	defer rows.Close()
	var out []FirmwareComponent
	for rows.Next() {
		var c FirmwareComponent
		var name, ver, mfg, sid, health, rel sql.NullString
		if err := rows.Scan(&c.DeviceID, &c.TS, &c.ID, &name, &ver, &mfg, &sid, &health, &c.Updateable, &rel); err != nil {
			return nil, fmt.Errorf("telemetry: scan firmware: %w", err)
		}
		c.Name = name.String
		c.Version = ver.String
		c.Manufacturer = mfg.String
		c.SoftwareID = sid.String
		c.Health = health.String
		c.ReleaseDate = rel.String
		out = append(out, c)
	}
	return out, rows.Err()
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
SELECT device_id, ts, sensor, value, unit, note
FROM sensor_readings
WHERE device_id = $1
  AND ts = (SELECT MAX(ts) FROM sensor_readings WHERE device_id = $1)
ORDER BY sensor
LIMIT $2`, deviceID, limit)
	} else {
		rows, err = p.db.QueryContext(ctx, `
SELECT device_id, ts, sensor, value, unit, note
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
		var unit, note sql.NullString
		var val sql.NullFloat64
		if err := rows.Scan(&r.DeviceID, &r.TS, &r.Sensor, &val, &unit, &note); err != nil {
			return nil, fmt.Errorf("telemetry: scan sensor: %w", err)
		}
		if val.Valid {
			v := val.Float64
			r.Value = &v
		}
		r.Unit = unit.String
		r.Note = note.String
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

func nullFloat(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

func newEventID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("evt-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

var _ Store = (*Postgres)(nil)
