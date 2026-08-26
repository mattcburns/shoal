package adapters

import (
	"fmt"
	"strings"

	"github.com/mattcburns/shoal/internal/common/models"
)

// Partial is a deterministic parse result before the confidence gate.
type Partial struct {
	Asset       models.NormalizedAsset
	Confidences []models.FieldConfidence
}

// Adapter parses one RawAssetInput kind into a partial asset.
type Adapter interface {
	// Kind is redfish_json | csv (photo has no deterministic adapter).
	Kind() string
	Parse(in models.RawAssetInput) (Partial, error)
}

// conf helper.
func conf(field string, c float64, evidence string) models.FieldConfidence {
	return models.FieldConfidence{
		Field:      field,
		Confidence: c,
		Source:     "deterministic",
		Evidence:   evidence,
	}
}

func firstString(m map[string]any, keys ...string) (string, string) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case string:
				if s := strings.TrimSpace(t); s != "" {
					return s, k
				}
			case fmt.Stringer:
				if s := strings.TrimSpace(t.String()); s != "" {
					return s, k
				}
			}
		}
		// case-insensitive scan
		for mk, mv := range m {
			if strings.EqualFold(mk, k) {
				if s, ok := mv.(string); ok {
					if s = strings.TrimSpace(s); s != "" {
						return s, mk
					}
				}
			}
		}
	}
	return "", ""
}
