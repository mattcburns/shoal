package job

import (
	"fmt"
	"os"
	"strings"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/deploy/iso"
)

// expandStages derives the stage list for a StartJobRequest (multi-stage design).
// M1: single os_install. M2: optional prep wipe + os_install.
// M3: offline seed fields on os_install (second_media / config_drive / auto).
// M5: operator_iso (ESXi/Windows) with coarse progress — no seed.
func expandStages(req models.StartJobRequest) ([]models.JobStage, error) {
	strategy, err := resolveInstallStrategy(req)
	if err != nil {
		return nil, err
	}
	if strategy == models.InstallStrategyScriptedISO {
		return nil, fmt.Errorf("job: install_strategy scripted_iso not implemented yet (use image_write, operator_iso, or simulate)")
	}

	seedDelivery, seedURL, err := normalizeSeedRequest(req, strategy)
	if err != nil {
		return nil, err
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

// normalizeSeedRequest applies defaults for seed fields before stage expansion.
// Final mode for "auto" is chosen at startStage once Virtual Media is listed.
func normalizeSeedRequest(req models.StartJobRequest, strategy string) (delivery, seedURL string, err error) {
	if strategy == models.InstallStrategyOperatorISO {
		// Operator media already contains unattended config.
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
			return "", fmt.Errorf("job: seed_delivery second_media needs ≥2 CD-capable Virtual Media slots (found %d); use prepare-time seed for image_write or dual-CD hardware", nCD)
		}
		return models.SeedDeliverySecondMedia, nil
	case models.SeedDeliveryConfigDrive:
		if strategy == models.InstallStrategyImageWrite {
			return "", fmt.Errorf("job: seed_delivery config_drive is incompatible with image_write")
		}
		// Prep-stage FAT writer for scripted_iso is not shipped yet.
		return "", fmt.Errorf("job: seed_delivery config_drive not implemented yet (M3 ships second_media; for image_write use prepare-ubuntu-cloud-payload)")
	case models.SeedDeliveryAuto:
		if nCD >= 2 && strings.TrimSpace(seedURL) != "" {
			return models.SeedDeliverySecondMedia, nil
		}
		if strategy == models.InstallStrategyImageWrite {
			return "", fmt.Errorf("job: seed_delivery auto: only one Virtual Media slot and image_write cannot use config_drive; bake seed with prepare-ubuntu-cloud-payload or attach seed_iso_url on a dual-CD BMC")
		}
		return "", fmt.Errorf("job: seed_delivery auto: config_drive fallback not implemented yet (need ≥2 CD slots for second_media, found %d)", nCD)
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
