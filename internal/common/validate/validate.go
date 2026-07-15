// Package validate implements hand-rolled domain validation (no third-party schema lib).
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

// ProvisioningProfile checks required fields and rejects secret keys in free-form steps lists.
func ProvisioningProfile(p models.ProvisioningProfile) error {
	if strings.TrimSpace(p.Ref) == "" {
		return fmt.Errorf("validate: profile ref is required")
	}
	if strings.TrimSpace(p.ISOBase) == "" {
		return fmt.Errorf("validate: profile iso_base is required")
	}
	for _, step := range append(append([]string{}, p.PostInstallSteps...), p.DestructSteps...) {
		if looksSecretStep(step) {
			return fmt.Errorf("validate: profile steps must not contain secret-like content")
		}
	}
	if len(p.DestructSteps) > 0 && !p.NeedsApproval {
		return fmt.Errorf("validate: destruct_steps require needs_approval=true")
	}
	return nil
}

func looksSecretStep(s string) bool {
	lower := strings.ToLower(s)
	for _, needle := range []string{"password", "passwd", "secret", "api_key", "apikey", "token=", "bearer "} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

// ProfileRequirements rejects secret-like keys in Extra.
func ProfileRequirements(r models.ProfileRequirements) error {
	if strings.TrimSpace(r.OSFamily) == "" {
		return fmt.Errorf("validate: os_family is required")
	}
	for k := range r.Extra {
		if redact.IsSensitiveKey(k) {
			return fmt.Errorf("validate: profile requirements extra must not contain secret key %q", k)
		}
	}
	return nil
}

// RawAssetInput checks kind and payload presence.
func RawAssetInput(in models.RawAssetInput) error {
	switch strings.ToLower(in.Kind) {
	case "redfish_json":
		if len(in.RedfishJSON) == 0 {
			return fmt.Errorf("validate: redfish_json payload is required")
		}
	case "csv":
		if len(in.CSVRow) == 0 {
			return fmt.Errorf("validate: csv_row payload is required")
		}
	case "photo":
		if strings.TrimSpace(in.PhotoBase64) == "" {
			return fmt.Errorf("validate: photo_base64 payload is required")
		}
	default:
		return fmt.Errorf("validate: unknown raw asset kind %q", in.Kind)
	}
	return nil
}

// StartJobRequest requires Phase 2 binding fields.
// ISOURL may be empty when ProfileRef is a non-spike stored profile so Deploy can
// resolve iso_base via SHOAL_ISO_BASE_URL (Phase 5c); Orchestrator fills it before BMC.
func StartJobRequest(r models.StartJobRequest) error {
	if strings.TrimSpace(r.DeviceID) == "" {
		return fmt.Errorf("validate: device_id is required")
	}
	if strings.TrimSpace(r.BMCEndpoint) == "" {
		return fmt.Errorf("validate: bmc_endpoint is required")
	}
	if strings.TrimSpace(r.ISOURL) == "" {
		ref := strings.TrimSpace(r.ProfileRef)
		// Phase 5c: non-spike profile may resolve; Phase 6a: BuildISO may produce URL.
		if (ref == "" || ref == "spike") && !r.BuildISO {
			return fmt.Errorf("validate: iso_url is required (or non-spike profile_ref / build_iso)")
		}
	}
	if strings.TrimSpace(r.SerialTarget) == "" {
		return fmt.Errorf("validate: serial_target is required")
	}
	hasUserPass := r.BMCUsername != "" || r.BMCPassword != ""
	hasRef := r.CredentialRef != ""
	if !hasUserPass && !hasRef {
		return fmt.Errorf("validate: bmc credentials or credential_ref is required")
	}
	return nil
}

// CancelJobRequest requires job_id.
func CancelJobRequest(r models.CancelJobRequest) error {
	if strings.TrimSpace(r.JobID) == "" {
		return fmt.Errorf("validate: job_id is required")
	}
	return nil
}
