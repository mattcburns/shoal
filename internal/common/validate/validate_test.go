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
	// M1: prep wipe not allowed
	prep := good
	prep.Prep = "wipe_only"
	if err := validate.StartJobRequest(prep); err == nil {
		t.Fatal("expected prep wipe error")
	}
	prep.Prep = "skip"
	if err := validate.StartJobRequest(prep); err != nil {
		t.Fatal(err)
	}
}
