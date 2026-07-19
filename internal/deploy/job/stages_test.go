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

func TestExpandStagesWipeOnly(t *testing.T) {
	stages, err := expandStages(models.StartJobRequest{
		ISOURL:     "http://example/os.iso",
		PrepISOURL: "http://example/prep.iso",
		Prep:       "wipe_only",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(stages) != 2 {
		t.Fatalf("len=%d", len(stages))
	}
	if stages[0].Kind != models.JobStageKindPrep || stages[0].MediaURL != "http://example/prep.iso" {
		t.Fatalf("prep %+v", stages[0])
	}
	if stages[1].Kind != models.JobStageKindOSInstall || stages[1].MediaURL != "http://example/os.iso" {
		t.Fatalf("os %+v", stages[1])
	}
}

func TestExpandStagesWipeOnlyNeedsPrepURL(t *testing.T) {
	t.Setenv("SHOAL_PREP_ISO_URL", "")
	_, err := expandStages(models.StartJobRequest{
		ISOURL: "http://example/os.iso",
		Prep:   "wipe_only",
	})
	if err == nil || !strings.Contains(err.Error(), "prep_iso") {
		t.Fatalf("got %v", err)
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

func TestExpandStagesSeedSecondMedia(t *testing.T) {
	stages, err := expandStages(models.StartJobRequest{
		ISOURL:          "http://example/os.iso",
		InstallStrategy: models.InstallStrategyImageWrite,
		SeedDelivery:    models.SeedDeliverySecondMedia,
		SeedISOURL:      "http://example/cidata.iso",
		OsFamily:        "ubuntu",
	})
	if err != nil {
		t.Fatal(err)
	}
	st := stages[0]
	if st.SeedDelivery != models.SeedDeliverySecondMedia {
		t.Fatalf("delivery=%s", st.SeedDelivery)
	}
	if st.SeedMediaURL != "http://example/cidata.iso" {
		t.Fatalf("seed=%s", st.SeedMediaURL)
	}
	if st.Family != "ubuntu" {
		t.Fatalf("family=%s", st.Family)
	}
}

func TestExpandStagesRejectsConfigDriveImageWrite(t *testing.T) {
	_, err := expandStages(models.StartJobRequest{
		ISOURL:          "http://example/os.iso",
		InstallStrategy: models.InstallStrategyImageWrite,
		SeedDelivery:    models.SeedDeliveryConfigDrive,
	})
	if err == nil || !strings.Contains(err.Error(), "config_drive") {
		t.Fatalf("got %v", err)
	}
}

func TestResolveSeedDelivery(t *testing.T) {
	// dual CD → second_media for auto
	got, err := resolveSeedDelivery(models.SeedDeliveryAuto, "http://s/seed.iso", models.InstallStrategyImageWrite, 2)
	if err != nil || got != models.SeedDeliverySecondMedia {
		t.Fatalf("auto dual: got %q err=%v", got, err)
	}
	// single CD + image_write → error (no config_drive)
	_, err = resolveSeedDelivery(models.SeedDeliveryAuto, "http://s/seed.iso", models.InstallStrategyImageWrite, 1)
	if err == nil {
		t.Fatal("expected auto+1cd+image_write error")
	}
	// explicit second_media needs 2 slots
	_, err = resolveSeedDelivery(models.SeedDeliverySecondMedia, "http://s/seed.iso", models.InstallStrategyImageWrite, 1)
	if err == nil {
		t.Fatal("expected second_media with 1 slot to fail")
	}
	got, err = resolveSeedDelivery(models.SeedDeliverySecondMedia, "http://s/seed.iso", models.InstallStrategyImageWrite, 2)
	if err != nil || got != models.SeedDeliverySecondMedia {
		t.Fatalf("second_media dual: %q %v", got, err)
	}
	// none
	got, err = resolveSeedDelivery(models.SeedDeliveryNone, "", models.InstallStrategyImageWrite, 1)
	if err != nil || got != models.SeedDeliveryNone {
		t.Fatalf("none: %q %v", got, err)
	}
	// config_drive + image_write
	_, err = resolveSeedDelivery(models.SeedDeliveryConfigDrive, "", models.InstallStrategyImageWrite, 1)
	if err == nil {
		t.Fatal("expected config_drive+image_write error")
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
