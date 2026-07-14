package poll_test

import (
	"context"
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/common/telemetry"
	"github.com/mattcburns/shoal/internal/core/ai"
	"github.com/mattcburns/shoal/internal/core/reconcile"
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
	rec, err := reconcile.New(&ai.Fake{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := poll.New(nil, store, func(redfish.Config) (redfish.BMC, error) { return fake, nil })
	p.Events = rec

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
	// second poll: no new SEL
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
	if evs[0].Component != "Temperature" && evs[0].Component != "thermal" {
		t.Fatalf("component=%q", evs[0].Component)
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
