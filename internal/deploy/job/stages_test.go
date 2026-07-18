package job

import (
	"strings"
	"testing"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/deploy/iso"
)

func TestExpandStagesSingleOSInstall(t *testing.T) {
	stages, err := expandStages(models.StartJobRequest{
		ISOURL:         "http://example/marker.iso",
		ISOInstallMode: iso.InstallModeAutoinstall,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(stages) != 1 {
		t.Fatalf("len=%d", len(stages))
	}
	st := stages[0]
	if st.Kind != models.JobStageKindOSInstall || st.ID != models.JobStageKindOSInstall {
		t.Fatalf("stage: %+v", st)
	}
	if st.Strategy != models.InstallStrategyImageWrite {
		t.Fatalf("strategy=%s", st.Strategy)
	}
	if st.MediaURL != "http://example/marker.iso" {
		t.Fatalf("media=%s", st.MediaURL)
	}
	if st.State != models.JobStageStatePending {
		t.Fatalf("state=%s", st.State)
	}
}

func TestExpandStagesSimulate(t *testing.T) {
	stages, err := expandStages(models.StartJobRequest{
		ISOURL:          "http://example/m.iso",
		InstallStrategy: models.InstallStrategySimulate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stages[0].Strategy != models.InstallStrategySimulate {
		t.Fatalf("got %s", stages[0].Strategy)
	}
}

func TestExpandStagesRejectsPrepWipe(t *testing.T) {
	_, err := expandStages(models.StartJobRequest{
		ISOURL: "http://example/m.iso",
		Prep:   "wipe_only",
	})
	if err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("want not implemented, got %v", err)
	}
}

func TestExpandStagesRejectsScriptedISO(t *testing.T) {
	_, err := expandStages(models.StartJobRequest{
		ISOURL:          "http://example/m.iso",
		InstallStrategy: models.InstallStrategyScriptedISO,
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSetStageState(t *testing.T) {
	stages := []models.JobStage{
		{ID: "os_install", State: models.JobStageStatePending},
	}
	out := setStageState(stages, "os_install", models.JobStageStateRunning, "WAITING_SOL", "")
	if out[0].State != models.JobStageStateRunning || out[0].Phase != "WAITING_SOL" {
		t.Fatalf("%+v", out[0])
	}
	if stages[0].State != models.JobStageStatePending {
		t.Fatal("original mutated")
	}
}
