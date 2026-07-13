//go:build integration

package telemetry_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/telemetry"
)

// Lab: SHOAL_TELEMETRY_DATABASE_URL=postgres://shoal:…@192.168.122.100:5433/shoal_telemetry?sslmode=disable
func TestLabPostgresStoreWriteList(t *testing.T) {
	dsn := os.Getenv("SHOAL_TELEMETRY_DATABASE_URL")
	if dsn == "" {
		t.Skip("SHOAL_TELEMETRY_DATABASE_URL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := telemetry.OpenAndMigrate(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	st := telemetry.NewPostgres(db)
	device := fmt.Sprintf("lab-p4a-%d", time.Now().UnixNano())
	ts := time.Now().UTC()

	if err := st.WriteEvent(ctx, models.NormalizedEvent{
		DeviceID:  device,
		EventType: "sel",
		Severity:  "info",
		Message:   "phase4a lab store probe",
		Timestamp: ts,
	}); err != nil {
		t.Fatalf("write event: %v", err)
	}
	if err := st.WriteSensor(ctx, telemetry.SensorReading{
		DeviceID: device,
		TS:       ts,
		Sensor:   "probe-temp",
		Value:    21.5,
		Unit:     "Cel",
	}); err != nil {
		t.Fatalf("write sensor: %v", err)
	}
	if err := st.WriteJobLog(ctx, "job-p4a-probe", ts, "phase4a probe line"); err != nil {
		t.Fatalf("write job log: %v", err)
	}

	evs, err := st.ListEvents(ctx, device, time.Time{}, 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(evs) == 0 || evs[0].Message != "phase4a lab store probe" {
		t.Fatalf("events: %+v", evs)
	}
	if evs[0].ID == "" {
		t.Fatal("expected event id")
	}

	sens, err := st.ListSensors(ctx, device, time.Time{}, 10)
	if err != nil {
		t.Fatalf("list sensors: %v", err)
	}
	if len(sens) == 0 || sens[0].Value != 21.5 {
		t.Fatalf("sensors: %+v", sens)
	}
	t.Logf("ok device=%s event_id=%s sensors=%d", device, evs[0].ID, len(sens))
}
