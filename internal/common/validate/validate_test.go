package validate_test

import (
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/validate"
)

func TestNormalizedAsset(t *testing.T) {
	err := validate.NormalizedAsset(models.NormalizedAsset{Serial: "S", BMCIP: "1.2.3.4"})
	if err != nil {
		t.Fatal(err)
	}
	if err := validate.NormalizedAsset(models.NormalizedAsset{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestNormalizationResultConfidence(t *testing.T) {
	r := models.NormalizationResult{
		Asset: models.NormalizedAsset{Serial: "S", BMCIP: "1.1.1.1"},
		Confidences: []models.FieldConfidence{
			{Field: "serial", Confidence: 0.9, Source: "deterministic"},
			{Field: "model", Confidence: 0.8, Source: "ai", Evidence: "Model: Foo"},
		},
	}
	if err := validate.NormalizationResult(r); err != nil {
		t.Fatal(err)
	}
	r.Confidences[1].Evidence = ""
	if err := validate.NormalizationResult(r); err == nil {
		t.Fatal("AI field without evidence should fail")
	}
}

func TestNormalizedEventRequiresDeviceID(t *testing.T) {
	if err := validate.NormalizedEvent(models.NormalizedEvent{Message: "x"}); err == nil {
		t.Fatal("expected error")
	}
	err := validate.NormalizedEvent(models.NormalizedEvent{
		DeviceID:  "d1",
		Message:   "ok",
		Timestamp: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStartJobRequest(t *testing.T) {
	good := models.StartJobRequest{
		DeviceID:     "n1",
		ISOURL:       "http://lab:8080/x.iso",
		BMCEndpoint:  "http://lab:8001",
		SerialTarget: "lab-node-1",
		BMCUsername:  "a",
		BMCPassword:  "b",
	}
	if err := validate.StartJobRequest(good); err != nil {
		t.Fatal(err)
	}
	bad := good
	bad.ISOURL = ""
	if err := validate.StartJobRequest(bad); err == nil {
		t.Fatal("expected error")
	}
	// Phase 5c: non-spike profile may omit iso_url (resolved later).
	resolve := good
	resolve.ISOURL = ""
	resolve.ProfileRef = "lab-1-ubuntu"
	if err := validate.StartJobRequest(resolve); err != nil {
		t.Fatal(err)
	}
	spike := resolve
	spike.ProfileRef = "spike"
	if err := validate.StartJobRequest(spike); err == nil {
		t.Fatal("spike still requires iso_url")
	}
	// Phase 6a: BuildISO may omit iso_url
	build := good
	build.ISOURL = ""
	build.ProfileRef = "spike"
	build.BuildISO = true
	if err := validate.StartJobRequest(build); err != nil {
		t.Fatal(err)
	}
	// M2: prep wipe requires approve + prep ISO
	prep := good
	prep.Prep = "wipe_only"
	if err := validate.StartJobRequest(prep); err == nil {
		t.Fatal("expected prep wipe without approve to fail")
	}
	prep.ApproveDestruct = true
	prep.PrepISOURL = "http://lab:8080/shoal-prep.iso"
	if err := validate.StartJobRequest(prep); err != nil {
		t.Fatal(err)
	}
	prep.Prep = "skip"
	if err := validate.StartJobRequest(prep); err != nil {
		t.Fatal(err)
	}
	// M3: config_drive forbidden with image_write
	seed := good
	seed.SeedDelivery = models.SeedDeliveryConfigDrive
	seed.InstallStrategy = models.InstallStrategyImageWrite
	if err := validate.StartJobRequest(seed); err == nil {
		t.Fatal("expected config_drive+image_write to fail")
	}
	// second_media needs seed URL
	seed = good
	seed.SeedDelivery = models.SeedDeliverySecondMedia
	t.Setenv("SHOAL_SEED_ISO_URL", "")
	if err := validate.StartJobRequest(seed); err == nil {
		t.Fatal("expected second_media without seed URL to fail")
	}
	seed.SeedISOURL = "http://lab:8080/cidata.iso"
	if err := validate.StartJobRequest(seed); err != nil {
		t.Fatal(err)
	}
	// none / empty seed is fine
	seed.SeedDelivery = ""
	seed.SeedISOURL = ""
	if err := validate.StartJobRequest(seed); err != nil {
		t.Fatal(err)
	}
	// bad seed delivery
	seed.SeedDelivery = "http_guest"
	if err := validate.StartJobRequest(seed); err == nil {
		t.Fatal("expected unknown seed_delivery to fail")
	}
	// M5 operator_iso
	op := good
	op.InstallStrategy = models.InstallStrategyOperatorISO
	op.OsFamily = models.OSFamilyESXi
	op.SerialTarget = "" // allowed for operator_iso
	if err := validate.StartJobRequest(op); err != nil {
		t.Fatal(err)
	}
	op.OsFamily = models.OSFamilyUbuntu
	if err := validate.StartJobRequest(op); err == nil {
		t.Fatal("expected ubuntu+operator_iso to fail")
	}
	op.OsFamily = models.OSFamilyESXi
	op.SeedDelivery = models.SeedDeliverySecondMedia
	op.SeedISOURL = "http://lab:8080/seed.iso"
	if err := validate.StartJobRequest(op); err == nil {
		t.Fatal("expected operator_iso with seed to fail")
	}
	// non-operator still needs serial
	noSerial := good
	noSerial.SerialTarget = ""
	if err := validate.StartJobRequest(noSerial); err == nil {
		t.Fatal("expected serial_target required without operator_iso")
	}
	// M4 scripted_iso flatcar
	fc := good
	fc.InstallStrategy = models.InstallStrategyScriptedISO
	fc.OsFamily = models.OSFamilyFlatcar
	fc.SerialTarget = ""
	fc.SeedDelivery = models.SeedDeliverySecondMedia
	fc.SeedISOURL = "http://lab:8080/ignition.iso"
	if err := validate.StartJobRequest(fc); err != nil {
		t.Fatal(err)
	}
	fc.SeedISOURL = ""
	t.Setenv("SHOAL_SEED_ISO_URL", "")
	if err := validate.StartJobRequest(fc); err == nil {
		t.Fatal("expected flatcar without seed to fail")
	}
	fc.SeedISOURL = "http://lab:8080/ignition.iso"
	fc.OsFamily = models.OSFamilyESXi
	if err := validate.StartJobRequest(fc); err == nil {
		t.Fatal("expected esxi+scripted_iso to fail")
	}
	// ubuntu scripted with seed
	ub := good
	ub.InstallStrategy = models.InstallStrategyScriptedISO
	ub.OsFamily = models.OSFamilyUbuntu
	ub.SeedDelivery = models.SeedDeliverySecondMedia
	ub.SeedISOURL = "http://lab:8080/cidata.iso"
	ub.SerialTarget = ""
	if err := validate.StartJobRequest(ub); err != nil {
		t.Fatal(err)
	}
}
