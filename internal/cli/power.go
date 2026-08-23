package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/mattcburns/shoal/internal/api"
	"github.com/mattcburns/shoal/internal/common/config"
	"github.com/mattcburns/shoal/internal/common/redfish"
)

type devicePower struct {
	cfg    config.Config
	newBMC redfish.Factory
}

func (d devicePower) Power(ctx context.Context, deviceID string, req api.DevicePowerRequest) (api.DevicePowerResult, error) {
	newBMC := d.newBMC
	if newBMC == nil {
		newBMC = redfish.NewBMC
	}
	bmc, err := newBMC(redfish.Config{
		BaseURL:  req.BMCEndpoint,
		Username: req.BMCUsername,
		Password: req.BMCPassword,
		AuthMode: d.cfg.RedfishAuthMode,
		TLSMode:  d.cfg.RedfishTLSMode,
		CAFile:   d.cfg.RedfishCAFile,
	})
	if err != nil {
		return api.DevicePowerResult{}, fmt.Errorf("bmc: %w", err)
	}
	if err := bmc.Open(ctx); err != nil {
		return api.DevicePowerResult{}, fmt.Errorf("bmc open: %w", err)
	}
	defer func() { _ = bmc.Close(context.Background()) }()

	if err := bmc.Reset(ctx, req.SystemID, req.ResetType); err != nil {
		return api.DevicePowerResult{}, err
	}
	info, err := bmc.GetSystem(ctx, req.SystemID)
	if err != nil {
		return api.DevicePowerResult{
			DeviceID:  deviceID,
			ResetType: req.ResetType,
			SystemID:  req.SystemID,
		}, nil
	}
	sysID := info.ID
	if strings.TrimSpace(sysID) == "" {
		sysID = req.SystemID
	}
	return api.DevicePowerResult{
		DeviceID:   deviceID,
		ResetType:  req.ResetType,
		PowerState: info.PowerState,
		SystemID:   sysID,
	}, nil
}
