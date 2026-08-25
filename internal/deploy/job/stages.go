package job

import (
	"fmt"
	"os"
	"strings"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/deploy/iso"
)

// expandStages derives the stage list for a StartJobRequest (multi-stage design).
// nCD is the BMC CD-capable Virtual Media count used to resolve seed_delivery=auto.
// Pass 0 to treat as unknown (defaults to 1 for conservative auto resolution).
func expandStages(req models.StartJobRequest, nCD int) ([]models.JobStage, error) {
	if strings.TrimSpace(strings.ToLower(req.Kind)) == models.JobKindDeprovision {
		return expandDeprovisionStages(req)
	}
	strategy, err := resolveInstallStrategy(req)
	if err != nil {
		return nil, err
	}
	if strategy == models.InstallStrategyScriptedISO {
		fam := strings.TrimSpace(strings.ToLower(req.OsFamily))
		switch fam {
		case models.OSFamilyFlatcar, models.OSFamilyUbuntu:
			// ok
		case "":
			return nil, fmt.Errorf("job: scripted_iso requires os_family flatcar or ubuntu")
		default:
			return nil, fmt.Errorf("job: scripted_iso does not support os_family %q", req.OsFamily)
		}
	}

	requested, seedURL, err := normalizeSeedRequest(req, strategy)
	if err != nil {
		return nil, err
	}
	if nCD <= 0 {
		nCD = 1
	}
	seedDelivery, err := resolveSeedDelivery(requested, seedURL, strategy, nCD)
	if err != nil {
		return nil, err
	}
	if strategy == models.InstallStrategyScriptedISO && strings.EqualFold(req.OsFamily, models.OSFamilyFlatcar) {
		if seedDelivery == models.SeedDeliveryNone {
			return nil, fmt.Errorf("job: scripted_iso flatcar requires offline Ignition seed (second_media or config_drive)")
		}
	}

	installMedia := strings.TrimSpace(req.ISOURL)
	osStage := models.JobStage{
		ID:           models.JobStageKindOSInstall,
		Kind:         models.JobStageKindOSInstall,
		Strategy:     strategy,
		Family:       strings.TrimSpace(strings.ToLower(req.OsFamily)),
		MediaURL:     installMedia,
		SeedMediaURL: seedURL,
		SeedDelivery: seedDelivery,
		State:        models.JobStageStatePending,
	}

	prep := strings.TrimSpace(strings.ToLower(req.Prep))
	// config_drive seed is written by the prep live image after wipe.
	if seedDelivery == models.SeedDeliveryConfigDrive {
		if prep == "" || prep == "skip" {
			if !req.ApproveDestruct {
				return nil, fmt.Errorf("job: seed_delivery config_drive requires prep wipe_only and approve_destruct (prep writes cidata after wipe)")
			}
			prep = "wipe_only"
		}
	}

	switch prep {
	case "", "skip":
		return []models.JobStage{osStage}, nil
	case "wipe_only":
		prepURL := strings.TrimSpace(req.PrepISOURL)
		if prepURL == "" {
			prepURL = strings.TrimSpace(os.Getenv("SHOAL_PREP_ISO_URL"))
		}
		if prepURL == "" {
			return nil, fmt.Errorf("job: prep wipe_only requires prep_iso_url or SHOAL_PREP_ISO_URL")
		}
		if installMedia == "" {
			return nil, fmt.Errorf("job: prep wipe_only requires iso_url for os_install stage")
		}
		prepStage := models.JobStage{
			ID:       models.JobStageKindPrep,
			Kind:     models.JobStageKindPrep,
			MediaURL: prepURL,
			State:    models.JobStageStatePending,
		}
		return []models.JobStage{prepStage, osStage}, nil
	case "full":
		return nil, fmt.Errorf("job: prep full not implemented (use wipe_only)")
	default:
		return nil, fmt.Errorf("job: unknown prep %q", req.Prep)
	}
}

// expandDeprovisionStages builds the single-stage wipe-only job for
// Kind=deprovision (docs/deprovision-design.md Key Decisions 1 and 5).
// Unlike the install path above, no os_install stage follows -- there is no
// InstallStrategy/ISOURL/OsFamily to resolve, and none of the seed-delivery
// logic applies. This is the whole reason Kind exists as an explicit
// discriminator rather than inferring "no install stage" from which fields
// the caller left empty.
func expandDeprovisionStages(req models.StartJobRequest) ([]models.JobStage, error) {
	prep := strings.TrimSpace(strings.ToLower(req.Prep))
	if prep != "wipe_only" {
		return nil, fmt.Errorf("job: kind=deprovision requires prep=wipe_only (got %q)", req.Prep)
	}
	prepURL := strings.TrimSpace(req.PrepISOURL)
	if prepURL == "" {
		prepURL = strings.TrimSpace(os.Getenv("SHOAL_PREP_ISO_URL"))
	}
	if prepURL == "" {
		return nil, fmt.Errorf("job: prep wipe_only requires prep_iso_url or SHOAL_PREP_ISO_URL")
	}
	prepStage := models.JobStage{
		ID:       models.JobStageKindPrep,
		Kind:     models.JobStageKindPrep,
		MediaURL: prepURL,
		State:    models.JobStageStatePending,
	}
	return []models.JobStage{prepStage}, nil
}

// normalizeSeedRequest applies defaults for seed fields before stage expansion.
func normalizeSeedRequest(req models.StartJobRequest, strategy string) (delivery, seedURL string, err error) {
	if strategy == models.InstallStrategyOperatorISO {
		return models.SeedDeliveryNone, "", nil
	}
	delivery = strings.TrimSpace(strings.ToLower(req.SeedDelivery))
	seedURL = strings.TrimSpace(req.SeedISOURL)
	if seedURL == "" {
		seedURL = strings.TrimSpace(os.Getenv("SHOAL_SEED_ISO_URL"))
	}
	if delivery == "" {
		if seedURL != "" {
			delivery = models.SeedDeliveryAuto
		} else {
			delivery = models.SeedDeliveryNone
		}
	}
	switch delivery {
	case models.SeedDeliveryNone:
		seedURL = ""
		return delivery, seedURL, nil
	case models.SeedDeliveryAuto, models.SeedDeliverySecondMedia, models.SeedDeliveryConfigDrive:
		// ok
	case models.SeedDeliverySingleISO:
		return "", "", fmt.Errorf("job: seed_delivery single_iso not implemented")
	default:
		return "", "", fmt.Errorf("job: unknown seed_delivery %q", req.SeedDelivery)
	}
	if delivery == models.SeedDeliveryConfigDrive && strategy == models.InstallStrategyImageWrite {
		return "", "", fmt.Errorf("job: seed_delivery config_drive is incompatible with image_write (full-disk dd destroys config-drive); use prepare-ubuntu-cloud-payload or second_media")
	}
	if delivery == models.SeedDeliverySecondMedia && seedURL == "" {
		return "", "", fmt.Errorf("job: seed_delivery second_media requires seed_iso_url or SHOAL_SEED_ISO_URL")
	}
	if delivery == models.SeedDeliveryAuto && seedURL == "" {
		// auto without seed URL is a no-op (nothing to deliver).
		// config_drive may still use seed baked into prep ISO only — use explicit config_drive.
		return models.SeedDeliveryNone, "", nil
	}
	return delivery, seedURL, nil
}

// resolveSeedDelivery picks the concrete offline seed mode given CD-capable media count.
// nCD is the number of CD-capable Virtual Media slots on the BMC.
func resolveSeedDelivery(requested, seedURL, strategy string, nCD int) (string, error) {
	req := strings.TrimSpace(strings.ToLower(requested))
	if req == "" || req == models.SeedDeliveryNone {
		return models.SeedDeliveryNone, nil
	}
	if strings.TrimSpace(seedURL) == "" && req != models.SeedDeliveryConfigDrive {
		return models.SeedDeliveryNone, nil
	}
	switch req {
	case models.SeedDeliverySecondMedia:
		if nCD < 2 {
			return "", fmt.Errorf("job: seed_delivery second_media needs ≥2 CD-capable Virtual Media slots (found %d); use config_drive with prep or dual-CD hardware", nCD)
		}
		return models.SeedDeliverySecondMedia, nil
	case models.SeedDeliveryConfigDrive:
		if strategy == models.InstallStrategyImageWrite {
			return "", fmt.Errorf("job: seed_delivery config_drive is incompatible with image_write")
		}
		if strategy != models.InstallStrategyScriptedISO && strategy != models.InstallStrategySimulate {
			return "", fmt.Errorf("job: seed_delivery config_drive requires install_strategy scripted_iso (got %q)", strategy)
		}
		return models.SeedDeliveryConfigDrive, nil
	case models.SeedDeliveryAuto:
		if nCD >= 2 && strings.TrimSpace(seedURL) != "" {
			return models.SeedDeliverySecondMedia, nil
		}
		if strategy == models.InstallStrategyImageWrite {
			return "", fmt.Errorf("job: seed_delivery auto: only one Virtual Media slot and image_write cannot use config_drive; bake seed with prepare-ubuntu-cloud-payload or use dual-CD second_media")
		}
		if strategy == models.InstallStrategyScriptedISO {
			// Single CD: prep writes cidata after wipe (seed ISO URL is optional if baked into prep).
			return models.SeedDeliveryConfigDrive, nil
		}
		return "", fmt.Errorf("job: seed_delivery auto: no mode for strategy %q with %d CD slots", strategy, nCD)
	default:
		return "", fmt.Errorf("job: unknown seed_delivery %q", requested)
	}
}

// installStrategyFromStages returns the os_install strategy (not prep's empty strategy).
func installStrategyFromStages(stages []models.JobStage) string {
	for _, s := range stages {
		if s.Kind == models.JobStageKindOSInstall && s.Strategy != "" {
			return s.Strategy
		}
	}
	for _, s := range stages {
		if s.Strategy != "" {
			return s.Strategy
		}
	}
	return ""
}

func resolveInstallStrategy(req models.StartJobRequest) (string, error) {
	if s := strings.TrimSpace(req.InstallStrategy); s != "" {
		return s, nil
	}
	mode := strings.TrimSpace(strings.ToLower(req.ISOInstallMode))
	switch mode {
	case iso.InstallModeWrite, iso.InstallModeAutoinstall:
		return models.InstallStrategyImageWrite, nil
	case iso.InstallModeSimulate:
		return models.InstallStrategySimulate, nil
	case "":
		return models.InstallStrategyImageWrite, nil
	default:
		return models.InstallStrategyImageWrite, nil
	}
}

// setStageState updates the stage with id in a copy of the list.
func setStageState(stages []models.JobStage, id, state, phase, errMsg string) []models.JobStage {
	out := make([]models.JobStage, len(stages))
	copy(out, stages)
	for i := range out {
		if out[i].ID == id {
			out[i].State = state
			if phase != "" {
				out[i].Phase = phase
			}
			if errMsg != "" {
				out[i].Error = errMsg
			}
			break
		}
	}
	return out
}

// stageIndex returns the index of stage id, or -1.
func stageIndex(stages []models.JobStage, id string) int {
	for i := range stages {
		if stages[i].ID == id {
			return i
		}
	}
	return -1
}
