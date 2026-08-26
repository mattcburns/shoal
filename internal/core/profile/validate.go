package profile

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/redact"
)

// ProvisioningProfile checks required fields and rejects secret keys in
// free-form steps lists.
func ProvisioningProfile(p models.ProvisioningProfile) error {
	if strings.TrimSpace(p.Ref) == "" {
		return fmt.Errorf("validate: profile ref is required")
	}
	if strings.TrimSpace(p.ISOBase) == "" && strings.TrimSpace(p.MediaURL) == "" {
		return fmt.Errorf("validate: profile iso_base or media_url is required")
	}
	for _, step := range append(append([]string{}, p.PostInstallSteps...), p.DestructSteps...) {
		if looksSecretStep(step) {
			return fmt.Errorf("validate: profile steps must not contain secret-like content")
		}
	}
	if len(p.DestructSteps) > 0 && !p.NeedsApproval {
		return fmt.Errorf("validate: destruct_steps require needs_approval=true")
	}
	prep := strings.TrimSpace(strings.ToLower(p.Prep))
	if prep == "wipe_only" && !p.NeedsApproval && len(p.DestructSteps) == 0 {
		// Wipe is destructive; require needs_approval when prep wipe is in the profile.
		return fmt.Errorf("validate: profile prep wipe_only requires needs_approval=true")
	}
	if s := strings.TrimSpace(p.InstallStrategy); s != "" {
		switch s {
		case models.InstallStrategySimulate, models.InstallStrategyImageWrite,
			models.InstallStrategyScriptedISO, models.InstallStrategyOperatorISO:
		default:
			return fmt.Errorf("validate: profile unknown install_strategy %q", s)
		}
	}
	if fam := strings.TrimSpace(strings.ToLower(p.OSFamily)); fam != "" {
		switch fam {
		case models.OSFamilyUbuntu, models.OSFamilyFlatcar, models.OSFamilyESXi, models.OSFamilyWindows:
		default:
			return fmt.Errorf("validate: profile unknown os_family %q", p.OSFamily)
		}
	}
	if seed := strings.TrimSpace(strings.ToLower(p.SeedDelivery)); seed != "" {
		switch seed {
		case models.SeedDeliveryNone, models.SeedDeliveryAuto,
			models.SeedDeliverySecondMedia, models.SeedDeliveryConfigDrive:
		case models.SeedDeliverySingleISO:
			return fmt.Errorf("validate: profile seed_delivery single_iso not implemented")
		default:
			return fmt.Errorf("validate: profile unknown seed_delivery %q", p.SeedDelivery)
		}
	}
	if w := strings.TrimSpace(strings.ToLower(p.WipeLevel)); w != "" && w != "discard" && w != "zero" {
		return fmt.Errorf("validate: profile wipe_level must be discard or zero")
	}
	// Guest HTTP answer-file patterns are forbidden (offline seed constraint).
	for _, field := range []string{p.ISOBase, p.MediaURL, p.SeedISOBase, p.SeedISOURL, p.PrepISOBase, p.PrepISOURL, p.EmbeddedPayload} {
		if hasGuestHTTPSeedPattern(field) {
			return fmt.Errorf("validate: profile must not contain guest HTTP seed URLs (ks=http, nocloud-net, ignition.config.url=http)")
		}
	}
	if to := strings.TrimSpace(p.StageTimeout); to != "" {
		if _, err := time.ParseDuration(to); err != nil {
			return fmt.Errorf("validate: profile stage_timeout: %w", err)
		}
	}
	return nil
}

func hasGuestHTTPSeedPattern(s string) bool {
	lower := strings.ToLower(s)
	needles := []string{
		"ks=http://", "ks=https://",
		"ignition.config.url=http://", "ignition.config.url=https://",
		"nocloud-net", "ds=nocloud-net",
	}
	for _, n := range needles {
		if strings.Contains(lower, n) {
			return true
		}
	}
	return false
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
