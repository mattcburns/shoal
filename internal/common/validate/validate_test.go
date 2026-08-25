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

func TestProvisioningProfileM6(t *testing.T) {
	good := models.ProvisioningProfile{
		Ref: "lab-1", ISOBase: "shoal-marker", InstallStrategy: models.InstallStrategyImageWrite,
		OSFamily: models.OSFamilyUbuntu, SeedDelivery: models.SeedDeliveryNone,
	}
	if err := validate.ProvisioningProfile(good); err != nil {
		t.Fatal(err)
	}
	// media_url without iso_base
	mediaOnly := models.ProvisioningProfile{
		Ref: "esxi", MediaURL: "http://lab:8080/esxi.iso",
		InstallStrategy: models.InstallStrategyOperatorISO, OSFamily: models.OSFamilyESXi,
	}
	if err := validate.ProvisioningProfile(mediaOnly); err != nil {
		t.Fatal(err)
	}
	// wipe requires needs_approval
	wipe := good
	wipe.Prep = "wipe_only"
	if err := validate.ProvisioningProfile(wipe); err == nil {
		t.Fatal("expected wipe without needs_approval to fail")
	}
	wipe.NeedsApproval = true
	if err := validate.ProvisioningProfile(wipe); err != nil {
		t.Fatal(err)
	}
	// guest HTTP seed pattern
	bad := good
	bad.SeedISOURL = "http://x?ignition.config.url=http://evil"
	// pattern is in string
	bad.EmbeddedPayload = "ignition.config.url=http://evil/"
	if err := validate.ProvisioningProfile(bad); err == nil {
		t.Fatal("expected guest HTTP pattern reject")
	}
	// neither media
	empty := models.ProvisioningProfile{Ref: "x"}
	if err := validate.ProvisioningProfile(empty); err == nil {
		t.Fatal("expected iso_base or media_url required")
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
	// Kind=deprovision: no iso_url needed, but prep=wipe_only + wipe_level required.
	dep := models.StartJobRequest{
		Kind:            models.JobKindDeprovision,
		DeviceID:        "n1",
		BMCEndpoint:     "http://lab:8001",
		SerialTarget:    "lab-node-1",
		BMCUsername:     "a",
		BMCPassword:     "b",
		Prep:            "wipe_only",
		PrepISOURL:      "http://lab:8080/shoal-prep.iso",
		WipeLevel:       "zero",
		ApproveDestruct: true,
	}
	if err := validate.StartJobRequest(dep); err != nil {
		t.Fatal(err)
	}
	noWipeLevel := dep
	noWipeLevel.WipeLevel = ""
	if err := validate.StartJobRequest(noWipeLevel); err == nil {
		t.Fatal("expected deprovision without wipe_level to fail")
	}
	noPrep := dep
	noPrep.Prep = "skip"
	if err := validate.StartJobRequest(noPrep); err == nil {
		t.Fatal("expected deprovision without prep=wipe_only to fail")
	}
}

func TestDeviceCredentials(t *testing.T) {
	if err := validate.DeviceCredentials("root", "x"); err != nil {
		t.Fatal(err)
	}
	if err := validate.DeviceCredentials("root", ""); err != nil {
		t.Fatal(err)
	}
	if err := validate.DeviceCredentials("", "x"); err == nil {
		t.Fatal("expected username required")
	}
}

func TestDevicePoll(t *testing.T) {
	if err := validate.DevicePoll("https://bmc"); err != nil {
		t.Fatal(err)
	}
	if err := validate.DevicePoll(""); err == nil {
		t.Fatal("expected missing endpoint")
	}
}

func TestDevicePower(t *testing.T) {
	if err := validate.DevicePower("On", "https://bmc"); err != nil {
		t.Fatal(err)
	}
	if err := validate.DevicePower("Explode", "https://bmc"); err == nil {
		t.Fatal("expected bad reset_type")
	}
	if err := validate.DevicePower("On", ""); err == nil {
		t.Fatal("expected missing endpoint")
	}
}
