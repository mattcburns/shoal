// Package fewshot stores operator-confirmed reconciliation examples for Core AI prompts.
// Discover confirms; Core appends/loads. No secrets allowed in stored input.
package fewshot

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/redact"
)

// PromptReconcileAsset is the learned-file key for asset reconciliation.
const PromptReconcileAsset = "reconcile_asset.v1"

// DefaultLoadLimit caps how many learned examples enter a prompt.
const DefaultLoadLimit = 12

// Example is one confirmed few-shot line (JSONL).
type Example struct {
	ID        string                     `json:"id"`
	CreatedAt time.Time                  `json:"created_at"`
	Prompt    string                     `json:"prompt"` // e.g. reconcile_asset.v1
	Kind      string                     `json:"kind"`   // redfish_json | csv | photo
	Input     map[string]any             `json:"input"`  // redacted raw / OCR text map
	Partial   *models.NormalizedAsset    `json:"partial,omitempty"`
	Output    models.NormalizationResult `json:"output"`
	Source    string                     `json:"source"` // operator_confirm
	DeviceID  string                     `json:"device_id,omitempty"`
}

// Store is the Core few-shot surface.
type Store interface {
	// Append persists ex and returns the stored record (with id/timestamps filled).
	Append(ctx context.Context, ex Example) (Example, error)
	// Load returns up to limit most recent examples for prompt (oldest→newest).
	Load(ctx context.Context, prompt string, limit int) ([]Example, error)
}

// ValidateExample ensures the example is safe to persist and structurally valid.
func ValidateExample(ex Example) error {
	if ex.Prompt == "" {
		return fmt.Errorf("fewshot: prompt name required")
	}
	if ex.Kind == "" {
		return fmt.Errorf("fewshot: kind required")
	}
	if ex.Input == nil {
		return fmt.Errorf("fewshot: input required")
	}
	if redact.ContainsSensitiveKey(ex.Input) {
		return fmt.Errorf("fewshot: input still contains sensitive keys")
	}
	if ex.Source == "" {
		return fmt.Errorf("fewshot: source required")
	}
	if ex.Output.Asset.Serial == "" || ex.Output.Asset.BMCIP == "" {
		return fmt.Errorf("fewshot: output asset requires serial and bmc_ip")
	}
	return nil
}

// FormatForPrompt renders examples as few-shot JSONL text for the reconcile prompt.
func FormatForPrompt(examples []Example) string {
	if len(examples) == 0 {
		return ""
	}
	var buf []byte
	for _, ex := range examples {
		type pair struct {
			Input  map[string]any             `json:"input"`
			Output models.NormalizationResult `json:"output"`
		}
		line, err := json.Marshal(pair{Input: ex.Input, Output: ex.Output})
		if err != nil {
			continue
		}
		buf = append(buf, line...)
		buf = append(buf, '\n')
	}
	return string(buf)
}
