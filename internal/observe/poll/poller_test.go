package poll_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/common/telemetry"
	"github.com/mattcburns/shoal/internal/observe/poll"
)

type watchMap map[string]bool

func (w watchMap) HasWatch(id string) bool { return w[id] }

func TestPollOnceWritesSELAndSensors(t *testing.T) {
	store := telemetry.NewMemory()
	fake := redfish.NewFake()
	ts := time.Now().UTC()
	fake.SEL = []redfish.SELEntry{
		{ID: "1", Message: "CPU thermal warning", Severity: "Warning", SensorType: "Temperature", Created: ts, ODataID: "/e/1"},
		{ID: "1", Message: "CPU thermal warning", Severity: "Warning", SensorType: "Temperature", Created: ts, ODataID: "/e/1"}, // dup
	}
	fake.Sensors = []redfish.SensorSample{
		{Name: "Inlet", Reading: 23, Units: "Cel", Kind: "temperature"},
	}
	// Deterministic path — no Core / no Fake AI.
	p := poll.New(nil, store, func(redfish.Config) (redfish.BMC, error) { return fake, nil })

	got, err := p.PollOnce(context.Background(), poll.Target{
		DeviceID: "dev-1",
		BMC:      redfish.Config{BaseURL: "http://fake"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.SELNew != 1 {
		t.Fatalf("sel written=%d want 1 (dedup)", got.SELNew)
	}
	if got.SensorsWritten != 1 {
		t.Fatalf("sensors=%d", got.SensorsWritten)
	}
	if got.PowerState != "Off" {
		t.Fatalf("power=%q", got.PowerState)
	}
	got, err = p.PollOnce(context.Background(), poll.Target{
		DeviceID: "dev-1",
		BMC:      redfish.Config{BaseURL: "http://fake"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.SELNew != 0 {
		t.Fatalf("second poll sel=%d", got.SELNew)
	}
	evs, _ := store.ListEvents(context.Background(), "dev-1", time.Time{}, 10)
	if len(evs) != 1 {
		t.Fatalf("events=%d", len(evs))
	}
	if evs[0].Severity != "warning" {
		t.Fatalf("severity=%q", evs[0].Severity)
	}
	if evs[0].Component != "Temperature" {
		t.Fatalf("component=%q", evs[0].Component)
	}
}

func TestPollOnceWritesFirmwareAndPower(t *testing.T) {
	store := telemetry.NewMemory()
	fake := redfish.NewFake()
	fake.Systems[0].PowerState = "On"
	fake.Firmware = []redfish.FirmwareComponent{
		{ID: "Installed-0-BIOS", Name: "BIOS", Version: "1.8.0", Manufacturer: "Dell Inc."},
		{ID: "Installed-1-iDRAC", Name: "iDRAC", Version: "7.30.10.50"},
	}
	p := poll.New(nil, store, func(redfish.Config) (redfish.BMC, error) { return fake, nil })
	got, err := p.PollOnce(context.Background(), poll.Target{
		DeviceID: "6", BMC: redfish.Config{BaseURL: "http://fake"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.FirmwareWritten != 2 || got.PowerState != "On" {
		t.Fatalf("%+v", got)
	}
	fw, err := store.ListFirmware(context.Background(), "6", 20)
	if err != nil || len(fw) != 2 {
		t.Fatalf("firmware %v %+v", err, fw)
	}
	pw, err := store.LatestPower(context.Background(), "6")
	if err != nil || pw.PowerState != "On" {
		t.Fatalf("power %v %+v", err, pw)
	}
}

func TestPollOnceWritesSELWhenSensorsFail(t *testing.T) {
	store := telemetry.NewMemory()
	fake := redfish.NewFake()
	ts := time.Now().UTC()
	fake.SEL = []redfish.SELEntry{
		{ID: "1", Message: "chassis closed", Severity: "OK", Created: ts, ODataID: "/e/1"},
	}
	fake.ListSensorsErr = errors.New("context deadline exceeded")
	p := poll.New(nil, store, func(redfish.Config) (redfish.BMC, error) { return fake, nil })

	got, err := p.PollOnce(context.Background(), poll.Target{
		DeviceID: "dev-1",
		BMC:      redfish.Config{BaseURL: "http://fake"},
	})
	if err == nil {
		t.Fatal("expected sensor error")
	}
	if !strings.Contains(err.Error(), "list sensors") {
		t.Fatalf("err=%v", err)
	}
	if got.SELNew != 1 {
		t.Fatalf("sel written=%d want 1", got.SELNew)
	}
	if got.SensorsWritten != 0 {
		t.Fatalf("sensors=%d", got.SensorsWritten)
	}
	evs, _ := store.ListEvents(context.Background(), "dev-1", time.Time{}, 10)
	if len(evs) != 1 {
		t.Fatalf("events=%d", len(evs))
	}
}

func TestPollOnceSurfacesWriteFailures(t *testing.T) {
	// nil store → hard error
	p := poll.New(nil, nil, func(redfish.Config) (redfish.BMC, error) { return redfish.NewFake(), nil })
	_, err := p.PollOnce(context.Background(), poll.Target{
		DeviceID: "d", BMC: redfish.Config{BaseURL: "http://x"},
	})
	if err == nil {
		t.Fatal("expected error without store")
	}
}

func TestIntervalElevatedWhenWatching(t *testing.T) {
	p := poll.New(nil, telemetry.NewMemory(), nil)
	p.IdleInterval = time.Minute
	p.WatchInterval = 10 * time.Second
	p.Watching = watchMap{"d1": true}
	if p.IntervalFor("d1") != 10*time.Second {
		t.Fatal(p.IntervalFor("d1"))
	}
	if p.IntervalFor("other") != time.Minute {
		t.Fatal(p.IntervalFor("other"))
	}
}
