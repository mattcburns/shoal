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

	selN, sensN, err := p.PollOnce(context.Background(), poll.Target{
		DeviceID: "dev-1",
		BMC:      redfish.Config{BaseURL: "http://fake"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if selN != 1 {
		t.Fatalf("sel written=%d want 1 (dedup)", selN)
	}
	if sensN != 1 {
		t.Fatalf("sensors=%d", sensN)
	}
	selN, _, err = p.PollOnce(context.Background(), poll.Target{
		DeviceID: "dev-1",
		BMC:      redfish.Config{BaseURL: "http://fake"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if selN != 0 {
		t.Fatalf("second poll sel=%d", selN)
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

func TestPollOnceWritesSELWhenSensorsFail(t *testing.T) {
	store := telemetry.NewMemory()
	fake := redfish.NewFake()
	ts := time.Now().UTC()
	fake.SEL = []redfish.SELEntry{
		{ID: "1", Message: "chassis closed", Severity: "OK", Created: ts, ODataID: "/e/1"},
	}
	fake.ListSensorsErr = errors.New("context deadline exceeded")
	p := poll.New(nil, store, func(redfish.Config) (redfish.BMC, error) { return fake, nil })

	selN, sensN, err := p.PollOnce(context.Background(), poll.Target{
		DeviceID: "dev-1",
		BMC:      redfish.Config{BaseURL: "http://fake"},
	})
	if err == nil {
		t.Fatal("expected sensor error")
	}
	if !strings.Contains(err.Error(), "list sensors") {
		t.Fatalf("err=%v", err)
	}
	if selN != 1 {
		t.Fatalf("sel written=%d want 1", selN)
	}
	if sensN != 0 {
		t.Fatalf("sensors=%d", sensN)
	}
	evs, _ := store.ListEvents(context.Background(), "dev-1", time.Time{}, 10)
	if len(evs) != 1 {
		t.Fatalf("events=%d", len(evs))
	}
}

func TestPollOnceSurfacesWriteFailures(t *testing.T) {
	// nil store → hard error
	p := poll.New(nil, nil, func(redfish.Config) (redfish.BMC, error) { return redfish.NewFake(), nil })
	_, _, err := p.PollOnce(context.Background(), poll.Target{
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
