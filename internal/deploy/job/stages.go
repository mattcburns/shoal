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
func expandStages(req models.StartJobRequest) ([]models.JobStage, error) {
	strategy, err := resolveInstallStrategy(req)
	if err != nil {
		return nil, err
	}
	switch strategy {
	case models.InstallStrategyScriptedISO, models.InstallStrategyOperatorISO:
		return nil, fmt.Errorf("job: install_strategy %q not implemented (use image_write or simulate)", strategy)
	}

	installMedia := strings.TrimSpace(req.ISOURL)
	osStage := models.JobStage{
		ID:           models.JobStageKindOSInstall,
		Kind:         models.JobStageKindOSInstall,
		Strategy:     strategy,
		MediaURL:     installMedia,
		SeedDelivery: "none",
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
