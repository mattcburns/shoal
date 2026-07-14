// Package telemetry is the Postgres-primary events/sensors/job_log store.
package telemetry

import (
	"context"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
)

// SensorReading is one numeric sensor sample for sensor_readings.
type SensorReading struct {
	DeviceID string
	TS       time.Time
	Sensor   string
	Value    float64
	Unit     string
}

// Store is the durable telemetry surface (events, sensors, job log lines).
// Job rows live in deploy/jobstore on the same DB.
type Store interface {
	WriteEvent(ctx context.Context, e models.NormalizedEvent) error
	WriteSensor(ctx context.Context, r SensorReading) error
	WriteJobLog(ctx context.Context, jobID string, ts time.Time, line string) error
	ListEvents(ctx context.Context, deviceID string, since time.Time, limit int) ([]models.NormalizedEvent, error)
	ListSensors(ctx context.Context, deviceID string, since time.Time, limit int) ([]SensorReading, error)
}
