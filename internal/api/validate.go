package api

import (
	"fmt"
	"strings"
)

// devicePowerResetTypes is the operator allow-list for POST /v1/devices/{id}/power.
var devicePowerResetTypes = map[string]struct{}{
	"On":               {},
	"ForceOff":         {},
	"ForceRestart":     {},
	"GracefulRestart":  {},
	"GracefulShutdown": {},
}

// validateDeviceCredentials requires a username. Password may be empty (keep existing).
//
// Named with a lowercase prefix (rather than DeviceCredentials, as in the original
// internal/common/validate package) because DeviceCredentials is already an exported
// interface type in this package (see credentials.go).
func validateDeviceCredentials(username, password string) error {
	_ = password
	if strings.TrimSpace(username) == "" {
		return fmt.Errorf("validate: username is required")
	}
	return nil
}

// validateDevicePoll checks a BMC endpoint for on-demand SEL/sensor poll.
//
// Lowercase for the same reason as validateDeviceCredentials: DevicePoll is already
// an exported interface type in this package (see poll.go).
func validateDevicePoll(bmcEndpoint string) error {
	if strings.TrimSpace(bmcEndpoint) == "" {
		return fmt.Errorf("validate: bmc_endpoint is required")
	}
	return nil
}

// validateDevicePower checks reset_type and BMC endpoint. Username/password may be
// empty (Shoal env defaults). Never inspects password contents.
//
// Lowercase for the same reason as validateDeviceCredentials: DevicePower is already
// an exported interface type in this package (see power.go).
func validateDevicePower(resetType, bmcEndpoint string) error {
	if strings.TrimSpace(bmcEndpoint) == "" {
		return fmt.Errorf("validate: bmc_endpoint is required")
	}
	rt := strings.TrimSpace(resetType)
	if _, ok := devicePowerResetTypes[rt]; !ok {
		return fmt.Errorf("validate: reset_type must be one of On, ForceOff, ForceRestart, GracefulRestart, GracefulShutdown")
	}
	return nil
}
