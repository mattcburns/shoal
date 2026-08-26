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
