package job_test

import (
	"testing"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/deploy/job"
)

func TestStartJobRequest(t *testing.T) {
	good := models.StartJobRequest{
		DeviceID:     "n1",
		ISOURL:       "http://lab:8080/x.iso",
		BMCEndpoint:  "http://lab:8001",
		SerialTarget: "lab-node-1",
		BMCUsername:  "a",
		BMCPassword:  "b",
	}
	if err := job.StartJobRequest(good); err != nil {
		t.Fatal(err)
	}
	bad := good
	bad.ISOURL = ""
	if err := job.StartJobRequest(bad); err == nil {
		t.Fatal("expected error")
	}
	// Phase 5c: non-spike profile may omit iso_url (resolved later).
	resolve := good
	resolve.ISOURL = ""
	resolve.ProfileRef = "lab-1-ubuntu"
	if err := job.StartJobRequest(resolve); err != nil {
		t.Fatal(err)
	}
	spike := resolve
	spike.ProfileRef = "spike"
	if err := job.StartJobRequest(spike); err == nil {
		t.Fatal("spike still requires iso_url")
	}
	// Phase 6a: BuildISO may omit iso_url
	build := good
	build.ISOURL = ""
	build.ProfileRef = "spike"
	build.BuildISO = true
	if err := job.StartJobRequest(build); err != nil {
		t.Fatal(err)
	}
	// M2: prep wipe requires approve + prep ISO
	prep := good
	prep.Prep = "wipe_only"
	if err := job.StartJobRequest(prep); err == nil {
		t.Fatal("expected prep wipe without approve to fail")
	}
	prep.ApproveDestruct = true
	prep.PrepISOURL = "http://lab:8080/shoal-prep.iso"
	if err := job.StartJobRequest(prep); err != nil {
		t.Fatal(err)
	}
	prep.Prep = "skip"
	if err := job.StartJobRequest(prep); err != nil {
		t.Fatal(err)
	}
	// M3: config_drive forbidden with image_write
	seed := good
	seed.SeedDelivery = models.SeedDeliveryConfigDrive
	seed.InstallStrategy = models.InstallStrategyImageWrite
	if err := job.StartJobRequest(seed); err == nil {
		t.Fatal("expected config_drive+image_write to fail")
	}
	// second_media needs seed URL
	seed = good
	seed.SeedDelivery = models.SeedDeliverySecondMedia
	t.Setenv("SHOAL_SEED_ISO_URL", "")
	if err := job.StartJobRequest(seed); err == nil {
		t.Fatal("expected second_media without seed URL to fail")
	}
	seed.SeedISOURL = "http://lab:8080/cidata.iso"
	if err := job.StartJobRequest(seed); err != nil {
		t.Fatal(err)
	}
	// none / empty seed is fine
	seed.SeedDelivery = ""
	seed.SeedISOURL = ""
	if err := job.StartJobRequest(seed); err != nil {
		t.Fatal(err)
	}
	// bad seed delivery
	seed.SeedDelivery = "http_guest"
	if err := job.StartJobRequest(seed); err == nil {
		t.Fatal("expected unknown seed_delivery to fail")
	}
	// M5 operator_iso
	op := good
	op.InstallStrategy = models.InstallStrategyOperatorISO
	op.OsFamily = models.OSFamilyESXi
	op.SerialTarget = "" // allowed for operator_iso
	if err := job.StartJobRequest(op); err != nil {
		t.Fatal(err)
	}
	op.OsFamily = models.OSFamilyUbuntu
	if err := job.StartJobRequest(op); err == nil {
		t.Fatal("expected ubuntu+operator_iso to fail")
	}
	op.OsFamily = models.OSFamilyESXi
	op.SeedDelivery = models.SeedDeliverySecondMedia
	op.SeedISOURL = "http://lab:8080/seed.iso"
	if err := job.StartJobRequest(op); err == nil {
		t.Fatal("expected operator_iso with seed to fail")
	}
	// non-operator still needs serial
	noSerial := good
	noSerial.SerialTarget = ""
	if err := job.StartJobRequest(noSerial); err == nil {
		t.Fatal("expected serial_target required without operator_iso")
	}
	// M4 scripted_iso flatcar
	fc := good
	fc.InstallStrategy = models.InstallStrategyScriptedISO
	fc.OsFamily = models.OSFamilyFlatcar
	fc.SerialTarget = ""
	fc.SeedDelivery = models.SeedDeliverySecondMedia
	fc.SeedISOURL = "http://lab:8080/ignition.iso"
	if err := job.StartJobRequest(fc); err != nil {
		t.Fatal(err)
	}
	fc.SeedISOURL = ""
	t.Setenv("SHOAL_SEED_ISO_URL", "")
	if err := job.StartJobRequest(fc); err == nil {
		t.Fatal("expected flatcar without seed to fail")
	}
	fc.SeedISOURL = "http://lab:8080/ignition.iso"
	fc.OsFamily = models.OSFamilyESXi
	if err := job.StartJobRequest(fc); err == nil {
		t.Fatal("expected esxi+scripted_iso to fail")
	}
	// ubuntu scripted with seed
	ub := good
	ub.InstallStrategy = models.InstallStrategyScriptedISO
	ub.OsFamily = models.OSFamilyUbuntu
	ub.SeedDelivery = models.SeedDeliverySecondMedia
	ub.SeedISOURL = "http://lab:8080/cidata.iso"
	ub.SerialTarget = ""
	if err := job.StartJobRequest(ub); err != nil {
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
	if err := job.StartJobRequest(dep); err != nil {
		t.Fatal(err)
	}
	noWipeLevel := dep
	noWipeLevel.WipeLevel = ""
	if err := job.StartJobRequest(noWipeLevel); err == nil {
		t.Fatal("expected deprovision without wipe_level to fail")
	}
	noPrep := dep
	noPrep.Prep = "skip"
	if err := job.StartJobRequest(noPrep); err == nil {
		t.Fatal("expected deprovision without prep=wipe_only to fail")
	}
}
