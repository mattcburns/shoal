// Package merge implements Discover hybrid merge + conflict policy.
package merge

import (
	"strings"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/discover/adapters"
	"github.com/mattcburns/shoal/internal/discover/gate"
)

// Results merges deterministic partial with optional AI result.
// Prefer deterministic on conflict; set NeedsReview on conflict or low confidence.
func Results(partial adapters.Partial, ai *models.NormalizationResult) models.NormalizationResult {
	out := models.NormalizationResult{
		Asset:       partial.Asset,
		Confidences: append([]models.FieldConfidence(nil), partial.Confidences...),
	}
	if ai == nil {
		out.NeedsReview = !gate.Accept(partial) || lowRequired(out)
		return out
	}

	// Start from AI asset, overlay deterministic non-empty fields.
	merged := ai.Asset
	conflict := false
	if partial.Asset.Serial != "" {
		if ai.Asset.Serial != "" && !strings.EqualFold(partial.Asset.Serial, ai.Asset.Serial) {
			conflict = true
		}
		merged.Serial = partial.Asset.Serial
	}
	if partial.Asset.BMCIP != "" {
		if ai.Asset.BMCIP != "" && partial.Asset.BMCIP != ai.Asset.BMCIP {
			conflict = true
		}
		merged.BMCIP = partial.Asset.BMCIP
	}
	if partial.Asset.Vendor != "" {
		if ai.Asset.Vendor != "" && !strings.EqualFold(partial.Asset.Vendor, ai.Asset.Vendor) {
			conflict = true
		}
		merged.Vendor = partial.Asset.Vendor
	}
	if partial.Asset.Model != "" {
		if ai.Asset.Model != "" && !strings.EqualFold(partial.Asset.Model, ai.Asset.Model) {
			conflict = true
		}
		merged.Model = partial.Asset.Model
	}
	if partial.Asset.CredentialRef != "" {
		merged.CredentialRef = partial.Asset.CredentialRef
	}
	out.Asset = merged

	// Union confidences: deterministic first, then AI fields not already present.
	seen := map[string]struct{}{}
	var confs []models.FieldConfidence
	for _, fc := range partial.Confidences {
		confs = append(confs, fc)
		seen[strings.ToLower(fc.Field)] = struct{}{}
	}
	for _, fc := range ai.Confidences {
		k := strings.ToLower(fc.Field)
		if _, ok := seen[k]; ok {
			continue
		}
		if fc.Source == "" {
			fc.Source = "ai"
		}
		confs = append(confs, fc)
		seen[k] = struct{}{}
	}
	out.Confidences = confs
	out.NeedsReview = conflict || ai.NeedsReview || lowRequired(out)
	return out
}

func lowRequired(r models.NormalizationResult) bool {
	for _, field := range []string{"serial", "bmc_ip"} {
		for _, fc := range r.Confidences {
			if strings.EqualFold(fc.Field, field) && fc.Confidence < gate.MinConfidence {
				return true
			}
		}
	}
	return false
}
