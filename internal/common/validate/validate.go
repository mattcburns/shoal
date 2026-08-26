// Package validate implements hand-rolled domain validation (no third-party schema lib).
//
// Only genuinely cross-domain validators live here: NormalizedAsset/FieldConfidence/
// NormalizationResult/NormalizedEvent are consumed by internal/discover, internal/core/reconcile,
// and internal/common/telemetry alike, so no single domain package owns them without
// creating a dependency in the wrong direction (in particular, internal/common/telemetry
// must not import internal/core or internal/discover). Domain-specific validators
// (raw asset input, provisioning profiles, job requests, device ops) have moved to
// their owning packages: internal/discover, internal/core/profile, internal/deploy/job,
// and internal/api respectively.
package validate

import (
	"fmt"
	"strings"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/redact"
)

// NormalizedAsset checks identity fields and that no password is present via JSON shape.
func NormalizedAsset(a models.NormalizedAsset) error {
	if strings.TrimSpace(a.Serial) == "" {
		return fmt.Errorf("validate: asset serial is required")
	}
	if strings.TrimSpace(a.BMCIP) == "" {
		return fmt.Errorf("validate: asset bmc_ip is required")
	}
	// credential_ref may be empty only before secrets are stashed; allow empty here
	// but reject path-like refs when set.
	if a.CredentialRef != "" {
		if strings.ContainsAny(a.CredentialRef, "/\\") {
			return fmt.Errorf("validate: invalid credential_ref")
		}
	}
	return nil
}

// FieldConfidence checks confidence bounds and AI evidence rules.
func FieldConfidence(fc models.FieldConfidence) error {
	if strings.TrimSpace(fc.Field) == "" {
		return fmt.Errorf("validate: confidence field name is required")
	}
	if fc.Confidence < 0 || fc.Confidence > 1 {
		return fmt.Errorf("validate: confidence for %q must be in [0,1]", fc.Field)
	}
	src := strings.ToLower(fc.Source)
	if src != "deterministic" && src != "ai" {
		return fmt.Errorf("validate: confidence source for %q must be deterministic or ai", fc.Field)
	}
	if src == "ai" && strings.TrimSpace(fc.Evidence) == "" {
		return fmt.Errorf("validate: AI confidence for %q requires evidence", fc.Field)
	}
	return nil
}

// NormalizationResult validates asset + confidences.
func NormalizationResult(r models.NormalizationResult) error {
	if err := NormalizedAsset(r.Asset); err != nil {
		return err
	}
	for _, fc := range r.Confidences {
		if err := FieldConfidence(fc); err != nil {
			return err
		}
	}
	return nil
}

// NormalizedEvent requires DeviceID and a message.
func NormalizedEvent(e models.NormalizedEvent) error {
	if strings.TrimSpace(e.DeviceID) == "" {
		return fmt.Errorf("validate: event device_id is required")
	}
	if strings.TrimSpace(e.Message) == "" {
		return fmt.Errorf("validate: event message is required")
	}
	if e.Raw != nil && redact.ContainsSensitiveKey(e.Raw) {
		return fmt.Errorf("validate: event raw still contains sensitive keys")
	}
	return nil
}
