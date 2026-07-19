// Package validate implements hand-rolled domain validation (no third-party schema lib).
package validate

import (
	"fmt"
	"os"
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
	strategy := strings.TrimSpace(r.InstallStrategy)
	operatorISO := strategy == models.InstallStrategyOperatorISO
	if strings.TrimSpace(r.SerialTarget) == "" && !operatorISO {
		return fmt.Errorf("validate: serial_target is required")
	}
	hasUserPass := r.BMCUsername != "" || r.BMCPassword != ""
	hasRef := r.CredentialRef != ""
	if !hasUserPass && !hasRef {
		return fmt.Errorf("validate: bmc credentials or credential_ref is required")
	}
	if strategy != "" {
		switch strategy {
		case models.InstallStrategySimulate, models.InstallStrategyImageWrite,
			models.InstallStrategyScriptedISO, models.InstallStrategyOperatorISO:
			// scripted_iso may still be rejected at expand
		default:
			return fmt.Errorf("validate: unknown install_strategy %q", strategy)
		}
	}
	if operatorISO {
		if strings.TrimSpace(r.ISOURL) == "" && !r.BuildISO {
			ref := strings.TrimSpace(r.ProfileRef)
			if ref == "" || ref == "spike" {
				return fmt.Errorf("validate: operator_iso requires iso_url (operator-supplied media)")
			}
		}
		fam := strings.TrimSpace(strings.ToLower(r.OsFamily))
		switch fam {
		case models.OSFamilyESXi, models.OSFamilyWindows:
			// ok
		case "":
			return fmt.Errorf("validate: operator_iso requires os_family esxi or windows")
		case models.OSFamilyUbuntu, models.OSFamilyFlatcar:
			return fmt.Errorf("validate: os_family %q is not valid with operator_iso (use image_write or scripted_iso)", r.OsFamily)
		default:
			return fmt.Errorf("validate: unknown os_family %q for operator_iso (want esxi or windows)", r.OsFamily)
		}
		seed := strings.TrimSpace(strings.ToLower(r.SeedDelivery))
		if seed != "" && seed != models.SeedDeliveryNone {
			return fmt.Errorf("validate: operator_iso does not use seed_delivery (config must be on the operator ISO; use none)")
		}
		if strings.TrimSpace(r.SeedISOURL) != "" {
			return fmt.Errorf("validate: operator_iso does not use seed_iso_url")
		}
		if r.StageTimeout < 0 {
			return fmt.Errorf("validate: stage_timeout must be non-negative")
		}
	}
	prep := strings.TrimSpace(strings.ToLower(r.Prep))
	switch prep {
	case "", "skip":
		// ok
	case "wipe_only":
		if !r.ApproveDestruct {
			return fmt.Errorf("validate: prep wipe_only requires approve_destruct")
		}
		prepISO := strings.TrimSpace(r.PrepISOURL)
		if prepISO == "" {
			prepISO = strings.TrimSpace(os.Getenv("SHOAL_PREP_ISO_URL"))
		}
		if prepISO == "" {
			return fmt.Errorf("validate: prep wipe_only requires prep_iso_url or SHOAL_PREP_ISO_URL")
		}
	case "full":
		return fmt.Errorf("validate: prep full not implemented (use wipe_only)")
	default:
		return fmt.Errorf("validate: unknown prep %q", r.Prep)
	}
	if w := strings.TrimSpace(strings.ToLower(r.WipeLevel)); w != "" && w != "discard" && w != "zero" {
		return fmt.Errorf("validate: wipe_level must be discard or zero")
	}

	// Multi-stage M3: offline seed delivery (no guest HTTP).
	seed := strings.TrimSpace(strings.ToLower(r.SeedDelivery))
	switch seed {
	case "", models.SeedDeliveryNone, models.SeedDeliveryAuto,
		models.SeedDeliverySecondMedia, models.SeedDeliveryConfigDrive:
		// ok
	case models.SeedDeliverySingleISO:
		return fmt.Errorf("validate: seed_delivery single_iso not implemented (use second_media or prepare-time seed)")
	default:
		return fmt.Errorf("validate: unknown seed_delivery %q", r.SeedDelivery)
	}
	if fam := strings.TrimSpace(strings.ToLower(r.OsFamily)); fam != "" {
		switch fam {
		case models.OSFamilyUbuntu, models.OSFamilyESXi, models.OSFamilyWindows:
			// ok (flatcar reserved for M4)
		case models.OSFamilyFlatcar:
			return fmt.Errorf("validate: os_family flatcar not implemented yet (M4)")
		default:
			if !operatorISO {
				return fmt.Errorf("validate: unknown os_family %q", r.OsFamily)
			}
		}
	}
	if seedURL := strings.TrimSpace(r.SeedISOURL); seedURL != "" {
		if !strings.HasPrefix(seedURL, "http://") && !strings.HasPrefix(seedURL, "https://") {
			return fmt.Errorf("validate: seed_iso_url must be an http(s) URL reachable by the BMC")
		}
	}
	// Full-disk image_write overwrites any config-drive partition; forbid the combination.
	if seed == models.SeedDeliveryConfigDrive && installStrategyIsImageWrite(r) {
		return fmt.Errorf("validate: seed_delivery config_drive is incompatible with install_strategy image_write (full-disk dd destroys the partition); use prepare-ubuntu-cloud-payload for offline seed, or second_media with a dual-CD BMC")
	}
	if seed == models.SeedDeliverySecondMedia && strings.TrimSpace(r.SeedISOURL) == "" {
		if strings.TrimSpace(os.Getenv("SHOAL_SEED_ISO_URL")) == "" {
			return fmt.Errorf("validate: seed_delivery second_media requires seed_iso_url or SHOAL_SEED_ISO_URL")
		}
	}
	return nil
}

// installStrategyIsImageWrite mirrors deploy/job strategy defaults for validation.
func installStrategyIsImageWrite(r models.StartJobRequest) bool {
	if s := strings.TrimSpace(r.InstallStrategy); s != "" {
		return s == models.InstallStrategyImageWrite
	}
	mode := strings.TrimSpace(strings.ToLower(r.ISOInstallMode))
	switch mode {
	case "write", "autoinstall", "":
		// empty mode defaults to image_write in expandStages
		return true
	case "simulate":
		return false
	default:
		return true
	}
}

// CancelJobRequest requires job_id.
func CancelJobRequest(r models.CancelJobRequest) error {
	if strings.TrimSpace(r.JobID) == "" {
		return fmt.Errorf("validate: job_id is required")
	}
	return nil
}
