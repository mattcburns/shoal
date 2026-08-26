package cli

import "testing"

// TestCmdObservePowerRejectsInvalidResetType pins the fix for a validation
// gap: cmdObservePower used to only check that -reset-type was non-empty and
// then dispatch straight to the BMC, unlike POST /v1/devices/{id}/power,
// which runs validate.DevicePower (reset_type enum + bmc_endpoint) before
// calling the power backend. An invalid reset_type could previously reach a
// real BMC unchecked via the CLI even though the HTTP API would reject the
// same value with 400. cmdObservePower now runs api.ValidateDevicePower
// (the same check, exported for CLI reuse) first, so this must fail fast
// (exit code 2, a usage/validation error) and
// never attempt to reach "the BMC" at 127.0.0.1:1 (a port that refuses
// immediately, but which the fixed code should never even dial).
func TestCmdObservePowerRejectsInvalidResetType(t *testing.T) {
	rc := cmdObservePower([]string{
		"-device-id", "dev1",
		"-bmc-url", "http://127.0.0.1:1",
		"-reset-type", "NotARealResetType",
	})
	if rc != 2 {
		t.Fatalf("cmdObservePower rc = %d, want 2 (validation rejection)", rc)
	}
}
