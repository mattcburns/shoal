package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/validate"
)

// Memory is an in-process Store for unit tests.
type Memory struct {
	mu      sync.RWMutex
	events  []models.NormalizedEvent
	sensors []SensorReading
	jobLogs []jobLogLine
}

type jobLogLine struct {
	JobID string
	TS    time.Time
	Line  string
}

// NewMemory returns an empty memory telemetry store.
func NewMemory() *Memory {
	return &Memory{}
}

// WriteEvent validates and appends an event (assigns ID if empty).
func (m *Memory) WriteEvent(_ context.Context, e models.NormalizedEvent) error {
	if err := validate.NormalizedEvent(e); err != nil {
		return err
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	} else {
		e.Timestamp = e.Timestamp.UTC()
	}
	if e.ID == "" {
		e.ID = newID()
	}
	// Do not retain Raw in durable form for MVP.
	e.Raw = nil
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, e)
	return nil
}

// WriteSensor appends a sensor reading.
func (m *Memory) WriteSensor(_ context.Context, r SensorReading) error {
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
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sensors = append(m.sensors, r)
	return nil
}

// WriteJobLog appends a job log line.
func (m *Memory) WriteJobLog(_ context.Context, jobID string, ts time.Time, line string) error {
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
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobLogs = append(m.jobLogs, jobLogLine{JobID: jobID, TS: ts, Line: line})
	return nil
}

// ListEvents returns events for deviceID at or after since, newest first, capped at limit.
func (m *Memory) ListEvents(_ context.Context, deviceID string, since time.Time, limit int) ([]models.NormalizedEvent, error) {
	if deviceID == "" {
		return nil, fmt.Errorf("telemetry: device_id required")
	}
	if limit <= 0 {
		limit = 100
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []models.NormalizedEvent
	for _, e := range m.events {
		if e.DeviceID != deviceID {
			continue
		}
		if !since.IsZero() && e.Timestamp.Before(since) {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Timestamp.After(out[j].Timestamp)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ListSensors returns sensor rows for deviceID at or after since, newest first.
func (m *Memory) ListSensors(_ context.Context, deviceID string, since time.Time, limit int) ([]SensorReading, error) {
	if deviceID == "" {
		return nil, fmt.Errorf("telemetry: device_id required")
	}
	if limit <= 0 {
		limit = 100
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []SensorReading
	for _, r := range m.sensors {
		if r.DeviceID != deviceID {
			continue
		}
		if !since.IsZero() && r.TS.Before(since) {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].TS.After(out[j].TS)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// JobLogCount returns lines for a job (test helper).
func (m *Memory) JobLogCount(jobID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, l := range m.jobLogs {
		if l.JobID == jobID {
			n++
		}
	}
	return n
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("evt-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

var _ Store = (*Memory)(nil)
