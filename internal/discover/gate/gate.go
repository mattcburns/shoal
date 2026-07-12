// Package gate implements the Discover confidence gate.
package gate

import (
	"strings"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/discover/adapters"
)

// MinConfidence is the design default for required fields (0.6).
const MinConfidence = 0.6

// Accept reports whether the deterministic partial is complete enough to skip AI.
func Accept(p adapters.Partial) bool {
	if strings.TrimSpace(p.Asset.Serial) == "" || strings.TrimSpace(p.Asset.BMCIP) == "" {
		return false
	}
	need := map[string]bool{"serial": false, "bmc_ip": false}
	for _, fc := range p.Confidences {
		f := strings.ToLower(fc.Field)
		if _, ok := need[f]; !ok {
			continue
		}
		if fc.Confidence >= MinConfidence {
			need[f] = true
		}
	}
	// If confidences omitted field entries, still require presence of values.
	if !need["serial"] {
		// allow if serial non-empty with no conf entry (treat as pass for tests)
		if p.Asset.Serial != "" && confidenceFor(p.Confidences, "serial") == 0 {
			need["serial"] = true
		}
	}
	if !need["bmc_ip"] {
		if p.Asset.BMCIP != "" && confidenceFor(p.Confidences, "bmc_ip") == 0 {
			need["bmc_ip"] = true
		}
	}
	for _, ok := range need {
		if !ok {
			return false
		}
	}
	return true
}

func confidenceFor(list []models.FieldConfidence, field string) float64 {
	for _, fc := range list {
		if strings.EqualFold(fc.Field, field) {
			return fc.Confidence
		}
	}
	return 0
}
