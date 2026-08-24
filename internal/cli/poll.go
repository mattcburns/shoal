package cli

import (
	"context"
	"fmt"

	"github.com/mattcburns/shoal/internal/api"
	"github.com/mattcburns/shoal/internal/common/config"
	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/observe/poll"
)

type devicePoll struct {
	p   *poll.Poller
	cfg config.Config
}

func (d devicePoll) Poll(ctx context.Context, deviceID string, req api.DevicePollRequest) (api.DevicePollResult, error) {
	if d.p == nil {
		return api.DevicePollResult{}, fmt.Errorf("poller not configured")
	}
	t := poll.Target{
		DeviceID: deviceID,
		SystemID: req.SystemID,
		BMC: redfish.Config{
			BaseURL:  req.BMCEndpoint,
			Username: req.BMCUsername,
			Password: req.BMCPassword,
			AuthMode: d.cfg.RedfishAuthMode,
			TLSMode:  d.cfg.RedfishTLSMode,
			CAFile:   d.cfg.RedfishCAFile,
		},
	}
	// Keep this device on the background interval after an on-demand poll.
	_ = d.p.SetTarget(t)
	res, err := d.p.PollOnce(ctx, t)
	out := api.DevicePollResult{
		DeviceID:        deviceID,
		SELNew:          res.SELNew,
		SensorsWritten:  res.SensorsWritten,
		FirmwareWritten: res.FirmwareWritten,
		PowerState:      res.PowerState,
	}
	if err != nil {
		return out, err
	}
	return out, nil
}

var _ api.DevicePoll = devicePoll{}
