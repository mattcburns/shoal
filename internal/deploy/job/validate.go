package job

import (
	"fmt"
	"os"
	"strings"

	"github.com/mattcburns/shoal/internal/common/models"
)

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
	deprovision := strings.TrimSpace(strings.ToLower(r.Kind)) == models.JobKindDeprovision
	// Kind=deprovision has no os_install stage (see expandDeprovisionStages): no
	// iso_url to require. Mirrors the same guard in orchestrator.Start.
	if !deprovision && strings.TrimSpace(r.ISOURL) == "" {
		ref := strings.TrimSpace(r.ProfileRef)
		// Phase 5c: non-spike profile may resolve; Phase 6a: BuildISO may produce URL.
		if (ref == "" || ref == "spike") && !r.BuildISO {
			return fmt.Errorf("validate: iso_url is required (or non-spike profile_ref / build_iso)")
		}
	}
	strategy := strings.TrimSpace(r.InstallStrategy)
	operatorISO := strategy == models.InstallStrategyOperatorISO
	scriptedISO := strategy == models.InstallStrategyScriptedISO
	serialTransport := strings.TrimSpace(r.SerialTransport)
	switch serialTransport {
	case "", "libvirt", "redfish_sol":
	default:
		return fmt.Errorf("validate: unknown serial_transport %q", serialTransport)
	}
	redfishSOL := serialTransport == "redfish_sol"
	// Coarse progress strategies may omit serial (operator_iso / scripted_iso), as may
	// redfish_sol (target is derived from bmc_endpoint, not serial_target).
	if strings.TrimSpace(r.SerialTarget) == "" && !operatorISO && !scriptedISO && !redfishSOL {
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
			// ok
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
	if scriptedISO {
		if strings.TrimSpace(r.ISOURL) == "" && !r.BuildISO {
			ref := strings.TrimSpace(r.ProfileRef)
			if ref == "" || ref == "spike" {
				return fmt.Errorf("validate: scripted_iso requires iso_url (installer media)")
			}
		}
		fam := strings.TrimSpace(strings.ToLower(r.OsFamily))
		switch fam {
		case models.OSFamilyFlatcar, models.OSFamilyUbuntu:
			// ok (M4 Flatcar Ignition; Ubuntu NoCloud second_media)
		case "":
			return fmt.Errorf("validate: scripted_iso requires os_family flatcar or ubuntu")
		case models.OSFamilyESXi, models.OSFamilyWindows:
			return fmt.Errorf("validate: os_family %q uses operator_iso, not scripted_iso", r.OsFamily)
		default:
			return fmt.Errorf("validate: unknown os_family %q for scripted_iso (want flatcar or ubuntu)", r.OsFamily)
		}
		seed := strings.TrimSpace(strings.ToLower(r.SeedDelivery))
		seedURL := strings.TrimSpace(r.SeedISOURL)
		if seedURL == "" {
			seedURL = strings.TrimSpace(os.Getenv("SHOAL_SEED_ISO_URL"))
		}
		// Flatcar must receive offline Ignition (never guest HTTP).
		if fam == models.OSFamilyFlatcar {
			if seed == "" || seed == models.SeedDeliveryNone {
				if seedURL == "" {
					return fmt.Errorf("validate: scripted_iso flatcar requires seed_delivery (second_media|auto) and seed_iso_url (offline Ignition)")
				}
			}
			if seed == models.SeedDeliveryNone {
				return fmt.Errorf("validate: scripted_iso flatcar cannot use seed_delivery=none (need offline Ignition seed)")
			}
			if seedURL == "" && (seed == models.SeedDeliverySecondMedia || seed == models.SeedDeliveryAuto || seed == "") {
				return fmt.Errorf("validate: scripted_iso flatcar requires seed_iso_url or SHOAL_SEED_ISO_URL")
			}
		}
		if r.StageTimeout < 0 {
			return fmt.Errorf("validate: stage_timeout must be non-negative")
		}
	}
	// image_write is not a Flatcar path in M4.
	if installStrategyIsImageWrite(r) && strings.TrimSpace(strings.ToLower(r.OsFamily)) == models.OSFamilyFlatcar {
		return fmt.Errorf("validate: os_family flatcar with image_write is not supported (use scripted_iso + offline Ignition seed)")
	}
	prep := strings.TrimSpace(strings.ToLower(r.Prep))
	if deprovision && prep != "wipe_only" {
		return fmt.Errorf("validate: kind=deprovision requires prep=wipe_only (got %q)", r.Prep)
	}
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
	w := strings.TrimSpace(strings.ToLower(r.WipeLevel))
	if w != "" && w != "discard" && w != "zero" {
		return fmt.Errorf("validate: wipe_level must be discard or zero")
	}
	// Key Decision 3 (docs/deprovision-design.md): unlike install jobs, where
	// wipe_level is optional and the prep script has its own default,
	// deprovision requires the caller to choose explicitly.
	if deprovision && w == "" {
		return fmt.Errorf("validate: kind=deprovision requires wipe_level (discard or zero)")
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
		case models.OSFamilyUbuntu, models.OSFamilyFlatcar, models.OSFamilyESXi, models.OSFamilyWindows:
			// ok
		default:
			if !operatorISO && !scriptedISO {
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
