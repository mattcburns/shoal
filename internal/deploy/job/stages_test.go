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
	}, 1)
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
	}, 1)
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
	}, 1)
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
	}, 1)
	if err == nil || !strings.Contains(err.Error(), "prep_iso") {
		t.Fatalf("got %v", err)
	}
}

func TestExpandStagesScriptedISOFlatcar(t *testing.T) {
	stages, err := expandStages(models.StartJobRequest{
		ISOURL:          "http://example/flatcar.iso",
		InstallStrategy: models.InstallStrategyScriptedISO,
		OsFamily:        models.OSFamilyFlatcar,
		SeedDelivery:    models.SeedDeliverySecondMedia,
		SeedISOURL:      "http://example/ignition.iso",
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if stages[0].Strategy != models.InstallStrategyScriptedISO {
		t.Fatalf("strategy=%s", stages[0].Strategy)
	}
	if stages[0].Family != models.OSFamilyFlatcar {
		t.Fatalf("family=%s", stages[0].Family)
	}
	if stages[0].SeedMediaURL != "http://example/ignition.iso" {
		t.Fatalf("seed=%s", stages[0].SeedMediaURL)
	}
}

func TestExpandStagesScriptedISOFlatcarNeedsSeed(t *testing.T) {
	t.Setenv("SHOAL_SEED_ISO_URL", "")
	_, err := expandStages(models.StartJobRequest{
		ISOURL:          "http://example/flatcar.iso",
		InstallStrategy: models.InstallStrategyScriptedISO,
		OsFamily:        models.OSFamilyFlatcar,
		SeedDelivery:    models.SeedDeliveryNone,
	}, 1)
	if err == nil {
		t.Fatal("expected missing seed error")
	}
}

func TestExpandStagesScriptedISORejectsESXi(t *testing.T) {
	_, err := expandStages(models.StartJobRequest{
		ISOURL:          "http://example/esxi.iso",
		InstallStrategy: models.InstallStrategyScriptedISO,
		OsFamily:        models.OSFamilyESXi,
	}, 1)
	if err == nil {
		t.Fatal("expected esxi+scripted_iso error")
	}
}

func TestExpandStagesOperatorISO(t *testing.T) {
	stages, err := expandStages(models.StartJobRequest{
		ISOURL:          "http://example/esxi.iso",
		InstallStrategy: models.InstallStrategyOperatorISO,
		OsFamily:        models.OSFamilyESXi,
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(stages) != 1 {
		t.Fatalf("len=%d", len(stages))
	}
	if stages[0].Strategy != models.InstallStrategyOperatorISO {
		t.Fatalf("strategy=%s", stages[0].Strategy)
	}
	if stages[0].Family != models.OSFamilyESXi {
		t.Fatalf("family=%s", stages[0].Family)
	}
	if stages[0].SeedDelivery != models.SeedDeliveryNone {
		t.Fatalf("seed=%s", stages[0].SeedDelivery)
	}
}

func TestExpandStagesOperatorISOWithPrep(t *testing.T) {
	stages, err := expandStages(models.StartJobRequest{
		ISOURL:          "http://example/esxi.iso",
		PrepISOURL:      "http://example/prep.iso",
		Prep:            "wipe_only",
		InstallStrategy: models.InstallStrategyOperatorISO,
		OsFamily:        models.OSFamilyWindows,
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(stages) != 2 {
		t.Fatalf("len=%d", len(stages))
	}
	if stages[1].Strategy != models.InstallStrategyOperatorISO {
		t.Fatalf("os strategy=%s", stages[1].Strategy)
	}
}

func TestExpandStagesSeedSecondMedia(t *testing.T) {
	stages, err := expandStages(models.StartJobRequest{
		ISOURL:          "http://example/os.iso",
		InstallStrategy: models.InstallStrategyImageWrite,
		SeedDelivery:    models.SeedDeliverySecondMedia,
		SeedISOURL:      "http://example/cidata.iso",
		OsFamily:        "ubuntu",
	}, 2)
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
	}, 1)
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
	// config_drive + scripted_iso
	got, err = resolveSeedDelivery(models.SeedDeliveryConfigDrive, "", models.InstallStrategyScriptedISO, 1)
	if err != nil || got != models.SeedDeliveryConfigDrive {
		t.Fatalf("config_drive scripted: %q %v", got, err)
	}
	// auto + scripted + 1 CD → config_drive
	got, err = resolveSeedDelivery(models.SeedDeliveryAuto, "http://s/seed.iso", models.InstallStrategyScriptedISO, 1)
	if err != nil || got != models.SeedDeliveryConfigDrive {
		t.Fatalf("auto single scripted: %q %v", got, err)
	}
}

func TestExpandStagesConfigDriveRequiresPrep(t *testing.T) {
	_, err := expandStages(models.StartJobRequest{
		ISOURL:          "http://example/live.iso",
		InstallStrategy: models.InstallStrategyScriptedISO,
		OsFamily:        models.OSFamilyUbuntu,
		SeedDelivery:    models.SeedDeliveryConfigDrive,
		Prep:            "skip",
	}, 1)
	if err == nil {
		t.Fatal("expected config_drive without approve/prep to fail")
	}
	stages, err := expandStages(models.StartJobRequest{
		ISOURL:          "http://example/live.iso",
		InstallStrategy: models.InstallStrategyScriptedISO,
		OsFamily:        models.OSFamilyUbuntu,
		SeedDelivery:    models.SeedDeliveryConfigDrive,
		PrepISOURL:      "http://example/prep.iso",
		ApproveDestruct: true,
		Prep:            "skip", // auto-upgraded to wipe_only
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(stages) != 2 {
		t.Fatalf("len=%d", len(stages))
	}
	if stages[1].SeedDelivery != models.SeedDeliveryConfigDrive {
		t.Fatalf("seed=%s", stages[1].SeedDelivery)
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
