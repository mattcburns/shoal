package telemetry_test

import (
	"context"
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/telemetry"
)

func TestMemoryWriteListEvents(t *testing.T) {
	st := telemetry.NewMemory()
	ctx := context.Background()
	ts := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	err := st.WriteEvent(ctx, models.NormalizedEvent{
		DeviceID:  "dev-1",
		EventType: "sel",
		Severity:  "warning",
		Component: "cpu",
		Message:   "thermal trip",
		Timestamp: ts,
	})
	if err != nil {
		t.Fatal(err)
	}
	// older event
	err = st.WriteEvent(ctx, models.NormalizedEvent{
		DeviceID:  "dev-1",
		Message:   "older",
		Timestamp: ts.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = st.WriteEvent(ctx, models.NormalizedEvent{
		DeviceID:  "other",
		Message:   "skip",
		Timestamp: ts,
	})

	got, err := st.ListEvents(ctx, "dev-1", time.Time{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].Message != "thermal trip" {
		t.Fatalf("want newest first, got %q", got[0].Message)
	}
	if got[0].ID == "" {
		t.Fatal("expected assigned id")
	}

	since := ts.Add(-30 * time.Minute)
	got, err = st.ListEvents(ctx, "dev-1", since, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Message != "thermal trip" {
		t.Fatalf("since filter: %+v", got)
	}
}

func TestMemoryWriteEventRejectsEmptyDevice(t *testing.T) {
	st := telemetry.NewMemory()
	err := st.WriteEvent(context.Background(), models.NormalizedEvent{Message: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestMemorySensorsAndJobLog(t *testing.T) {
	st := telemetry.NewMemory()
	ctx := context.Background()
	ts := time.Now().UTC()
	if err := st.WriteSensor(ctx, telemetry.SensorReading{
		DeviceID: "d1", TS: ts, Sensor: "Inlet Temp", Value: telemetry.SensorValue(24.5), Unit: "Cel",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteJobLog(ctx, "job-1", ts, "line1"); err != nil {
		t.Fatal(err)
	}
	sensors, err := st.ListSensors(ctx, "d1", time.Time{}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(sensors) != 1 || sensors[0].Value == nil || *sensors[0].Value != 24.5 {
		t.Fatalf("%+v", sensors)
	}
	old := ts.Add(-time.Minute)
	if err := st.WriteSensor(ctx, telemetry.SensorReading{
		DeviceID: "d1", TS: old, Sensor: "CPU1 VCCIN PG", Value: telemetry.SensorValue(0), Unit: "V",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteSensor(ctx, telemetry.SensorReading{
		DeviceID: "d1", TS: ts, Sensor: "Pwr Consumption", Value: telemetry.SensorValue(30), Unit: "W",
	}); err != nil {
		t.Fatal(err)
	}
	snap, err := st.ListSensors(ctx, "d1", time.Time{}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap) != 2 {
		t.Fatalf("snapshot want 2 latest-poll rows, got %+v", snap)
	}
	for _, r := range snap {
		if r.Sensor == "CPU1 VCCIN PG" {
			t.Fatalf("stale PG rail in snapshot: %+v", snap)
		}
	}
	if err := st.WriteSensor(ctx, telemetry.SensorReading{
		DeviceID: "d2", TS: ts, Sensor: "CPU1 Temp", Note: "No reading while host is off",
	}); err != nil {
		t.Fatal(err)
	}
	absent, err := st.ListSensors(ctx, "d2", time.Time{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(absent) != 1 || absent[0].Value != nil || absent[0].Note == "" {
		t.Fatalf("absent reading: %+v", absent)
	}
	if st.JobLogCount("job-1") != 1 {
		t.Fatal("job log")
	}
}

func TestMemoryListJobLog(t *testing.T) {
	st := telemetry.NewMemory()
	ctx := context.Background()
	base := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	if err := st.WriteJobLog(ctx, "job-1", base, "first"); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteJobLog(ctx, "job-1", base.Add(time.Minute), "second"); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteJobLog(ctx, "other-job", base, "skip"); err != nil {
		t.Fatal(err)
	}

	got, err := st.ListJobLog(ctx, "job-1", time.Time{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].Line != "first" || got[1].Line != "second" {
		t.Fatalf("want oldest-first [first second], got %+v", got)
	}

	since, err := st.ListJobLog(ctx, "job-1", base.Add(30*time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(since) != 1 || since[0].Line != "second" {
		t.Fatalf("since filter: %+v", since)
	}

	capped, err := st.ListJobLog(ctx, "job-1", time.Time{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != 1 || capped[0].Line != "first" {
		t.Fatalf("limit: %+v", capped)
	}
}

func TestMemoryFirmwareAndPower(t *testing.T) {
	st := telemetry.NewMemory()
	ctx := context.Background()
	ts := time.Now().UTC()
	if err := st.WriteFirmware(ctx, telemetry.FirmwareComponent{
		DeviceID: "d1", TS: ts, ID: "BIOS", Name: "BIOS", Version: "1.0",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.WritePower(ctx, telemetry.PowerReading{DeviceID: "d1", TS: ts, PowerState: "On"}); err != nil {
		t.Fatal(err)
	}
	fw, err := st.ListFirmware(ctx, "d1", 10)
	if err != nil || len(fw) != 1 || fw[0].Version != "1.0" {
		t.Fatalf("%v %+v", err, fw)
	}
	pw, err := st.LatestPower(ctx, "d1")
	if err != nil || pw.PowerState != "On" {
		t.Fatalf("%v %+v", err, pw)
	}
	if err := st.WritePower(ctx, telemetry.PowerReading{DeviceID: "d1", TS: ts.Add(time.Second), PowerState: "Off"}); err != nil {
		t.Fatal(err)
	}
	pw, _ = st.LatestPower(ctx, "d1")
	if pw.PowerState != "Off" {
		t.Fatalf("upsert %+v", pw)
	}
}

func TestMemoryListJobLogRequiresJobID(t *testing.T) {
	st := telemetry.NewMemory()
	if _, err := st.ListJobLog(context.Background(), "", time.Time{}, 10); err == nil {
		t.Fatal("expected error for empty job_id")
	}
}
