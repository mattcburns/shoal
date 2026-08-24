package cli

import (
	"context"
	"testing"

	"github.com/mattcburns/shoal/internal/api"
	"github.com/mattcburns/shoal/internal/common/config"
	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/common/telemetry"
	"github.com/mattcburns/shoal/internal/observe/poll"
)

func TestDevicePollSetsTargetAndWritesSensors(t *testing.T) {
	store := telemetry.NewMemory()
	fake := redfish.NewFake()
	fake.Sensors = []redfish.SensorSample{
		{Name: "Inlet", Reading: 21, Units: "Cel", Kind: "temperature", HasReading: true},
	}
	p := poll.New(nil, store, func(redfish.Config) (redfish.BMC, error) { return fake, nil })
	d := devicePoll{p: p, cfg: config.Config{}}
	out, err := d.Poll(context.Background(), "6", api.DevicePollRequest{BMCEndpoint: "https://bmc"})
	if err != nil {
		t.Fatal(err)
	}
	if out.SensorsWritten != 1 {
		t.Fatalf("%+v", out)
	}
	if len(p.Targets()) != 1 || p.Targets()[0].DeviceID != "6" {
		t.Fatalf("targets %+v", p.Targets())
	}
}
