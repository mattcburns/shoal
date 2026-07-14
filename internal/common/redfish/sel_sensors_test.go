package redfish_test

import (
	"context"
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/common/redfish"
)

func TestFakeListSELAndSensors(t *testing.T) {
	f := redfish.NewFake()
	ts := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	f.SEL = []redfish.SELEntry{
		{ID: "1", Message: "old", Created: ts.Add(-2 * time.Hour), Severity: "OK"},
		{ID: "2", Message: "new thermal", Created: ts, Severity: "Warning", SensorType: "Temperature"},
	}
	f.Sensors = []redfish.SensorSample{
		{Name: "Inlet", Reading: 22, Units: "Cel", Kind: "temperature"},
	}

	got, err := f.ListSEL(context.Background(), "1", redfish.SELOptions{Since: ts.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "2" {
		t.Fatalf("sel filter: %+v", got)
	}

	sensors, err := f.ListSensors(context.Background(), "1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sensors) != 1 || sensors[0].Name != "Inlet" {
		t.Fatalf("%+v", sensors)
	}
}

func TestFakeListSELMaxEntries(t *testing.T) {
	f := redfish.NewFake()
	for i := 0; i < 5; i++ {
		f.SEL = append(f.SEL, redfish.SELEntry{ID: string(rune('a' + i)), Message: "m"})
	}
	got, err := f.ListSEL(context.Background(), "", redfish.SELOptions{MaxEntries: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
}

func TestFakeImplementsBMC(t *testing.T) {
	var _ redfish.BMC = redfish.NewFake()
}
