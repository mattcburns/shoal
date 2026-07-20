// Package telemetry is the Postgres-primary events/sensors/job_log store.
package telemetry

import (
	"context"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
)

// SensorReading is one numeric sensor sample for sensor_readings.
type SensorReading struct {
	DeviceID string    `json:"device_id"`
	TS       time.Time `json:"ts"`
	Sensor   string    `json:"sensor"`
	Value    float64   `json:"value"`
	Unit     string    `json:"unit,omitempty"`
}

// JobLogLine is one durable job_log row.
type JobLogLine struct {
	JobID string    `json:"job_id"`
	TS    time.Time `json:"ts"`
	Line  string    `json:"line"`
}

// Store is the durable telemetry surface (events, sensors, job log lines).
// Job rows live in deploy/jobstore on the same DB.
type Store interface {
	WriteEvent(ctx context.Context, e models.NormalizedEvent) error
	WriteSensor(ctx context.Context, r SensorReading) error
	WriteJobLog(ctx context.Context, jobID string, ts time.Time, line string) error
	ListEvents(ctx context.Context, deviceID string, since time.Time, limit int) ([]models.NormalizedEvent, error)
	ListSensors(ctx context.Context, deviceID string, since time.Time, limit int) ([]SensorReading, error)
	// ListJobLog returns job_log lines oldest-first (a log reads chronologically,
	// unlike events/sensors which are newest-first "recent activity" feeds).
	ListJobLog(ctx context.Context, jobID string, since time.Time, limit int) ([]JobLogLine, error)
}
