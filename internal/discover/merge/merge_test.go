package merge_test

import (
	"testing"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/discover/adapters"
	"github.com/mattcburns/shoal/internal/discover/merge"
)

func TestMergePrefersDeterministic(t *testing.T) {
	partial := adapters.Partial{
		Asset: models.NormalizedAsset{Serial: "DET", BMCIP: "1.1.1.1", Vendor: "Dell"},
		Confidences: []models.FieldConfidence{
			{Field: "serial", Confidence: 0.95, Source: "deterministic"},
			{Field: "bmc_ip", Confidence: 0.95, Source: "deterministic"},
		},
	}
	ai := &models.NormalizationResult{
		Asset: models.NormalizedAsset{Serial: "AI", BMCIP: "1.1.1.1", Vendor: "HPE", Model: "X"},
		Confidences: []models.FieldConfidence{
			{Field: "model", Confidence: 0.8, Source: "ai", Evidence: "x"},
		},
	}
	got := merge.Results(partial, ai)
	if got.Asset.Serial != "DET" {
		t.Fatalf("serial %q", got.Asset.Serial)
	}
	if got.Asset.Model != "X" {
		t.Fatalf("model %q", got.Asset.Model)
	}
	if !got.NeedsReview {
		t.Fatal("expected needs_review on serial conflict")
	}
}
