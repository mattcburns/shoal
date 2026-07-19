package job

import (
	"fmt"
	"strings"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/deploy/iso"
)

// expandStages derives the stage list for a StartJobRequest (multi-stage design M1).
// M1 always returns a single os_install stage; multi-stage expansion lands in M2+.
func expandStages(req models.StartJobRequest) ([]models.JobStage, error) {
	if p := strings.TrimSpace(strings.ToLower(req.Prep)); p != "" && p != "skip" {
		return nil, fmt.Errorf("job: prep %q not implemented (M1 allows only skip)", req.Prep)
	}
	strategy, err := resolveInstallStrategy(req)
	if err != nil {
		return nil, err
	}
	// M1: scripted_iso / operator_iso are accepted by validate but not expanded yet.
	switch strategy {
	case models.InstallStrategyScriptedISO, models.InstallStrategyOperatorISO:
		return nil, fmt.Errorf("job: install_strategy %q not implemented in M1 (use image_write or simulate)", strategy)
	}

	media := strings.TrimSpace(req.ISOURL)
	stage := models.JobStage{
		ID:           models.JobStageKindOSInstall,
		Kind:         models.JobStageKindOSInstall,
		Strategy:     strategy,
		MediaURL:     media,
		SeedDelivery: "none",
		State:        models.JobStageStatePending,
	}
	return []models.JobStage{stage}, nil
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
		// Default: treat as image_write-compatible media attach (7a cloud ISO, marker write, etc.).
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
