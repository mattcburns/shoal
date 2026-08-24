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
	// Value is nil when the BMC listed the sensor but did not return a reading.
	Value *float64 `json:"value"`
	Unit  string   `json:"unit,omitempty"`
	Note  string   `json:"note,omitempty"`
}

// SensorValue is a helper for tests and call sites with a known numeric reading.
func SensorValue(v float64) *float64 { return &v }

// JobLogLine is one durable job_log row.
type JobLogLine struct {
	JobID string    `json:"job_id"`
	TS    time.Time `json:"ts"`
	Line  string    `json:"line"`
}

// PowerReading is the latest polled host power state for a device.
type PowerReading struct {
	DeviceID   string    `json:"device_id"`
	TS         time.Time `json:"ts"`
	PowerState string    `json:"power_state"`
}

// FirmwareComponent is one firmware inventory row from a poll snapshot.
type FirmwareComponent struct {
	DeviceID     string    `json:"device_id"`
	TS           time.Time `json:"ts"`
	ID           string    `json:"id"`
	Name         string    `json:"name,omitempty"`
	Version      string    `json:"version,omitempty"`
	Manufacturer string    `json:"manufacturer,omitempty"`
	SoftwareID   string    `json:"software_id,omitempty"`
	Health       string    `json:"health,omitempty"`
	Updateable   bool      `json:"updateable"`
	ReleaseDate  string    `json:"release_date,omitempty"`
}

// Store is the durable telemetry surface (events, sensors, job log lines).
// Job rows live in deploy/jobstore on the same DB.
type Store interface {
	WriteEvent(ctx context.Context, e models.NormalizedEvent) error
	WriteSensor(ctx context.Context, r SensorReading) error
	WriteJobLog(ctx context.Context, jobID string, ts time.Time, line string) error
	WritePower(ctx context.Context, r PowerReading) error
	WriteFirmware(ctx context.Context, c FirmwareComponent) error
	LatestPower(ctx context.Context, deviceID string) (PowerReading, error)
	ListFirmware(ctx context.Context, deviceID string, limit int) ([]FirmwareComponent, error)
	ListEvents(ctx context.Context, deviceID string, since time.Time, limit int) ([]models.NormalizedEvent, error)
	// ListSensors returns sensor rows for a device. When since is zero it is the
	// latest poll snapshot (all readings that share the device's max ts), so a
	// UI tab does not mix in older rails from previous polls. When since is set,
	// it is a newest-first time series at or after that instant.
	ListSensors(ctx context.Context, deviceID string, since time.Time, limit int) ([]SensorReading, error)
	// ListJobLog returns job_log lines oldest-first (a log reads chronologically,
	// unlike events/sensors which are newest-first "recent activity" feeds).
	ListJobLog(ctx context.Context, jobID string, since time.Time, limit int) ([]JobLogLine, error)
}
