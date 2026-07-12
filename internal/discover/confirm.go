package discover

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/redact"
	"github.com/mattcburns/shoal/internal/common/validate"
	"github.com/mattcburns/shoal/internal/core/fewshot"
)

// ConfirmRequest is a stateless operator acceptance of a normalization result.
// Learning only — does not re-write NetBox.
type ConfirmRequest struct {
	Kind     string                     `json:"kind"`  // redfish_json | csv | photo
	Input    map[string]any             `json:"input"` // redacted raw / description map
	Partial  *models.NormalizedAsset    `json:"partial,omitempty"`
	Result   models.NormalizationResult `json:"result"`
	DeviceID string                     `json:"device_id,omitempty"`
}

// ConfirmResult is returned after a successful learn.
type ConfirmResult struct {
	Learned bool   `json:"learned"`
	ID      string `json:"id,omitempty"`
	Prompt  string `json:"prompt"`
}

// Confirm appends an operator-confirmed example to the few-shot store.
func (s *Service) Confirm(ctx context.Context, req ConfirmRequest) (ConfirmResult, error) {
	if s.FewShot == nil {
		return ConfirmResult{}, fmt.Errorf("discover: few-shot store not configured (set SHOAL_FEWSHOT_DIR)")
	}
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	switch kind {
	case "redfish_json", "csv", "photo":
	default:
		return ConfirmResult{}, fmt.Errorf("discover: confirm kind must be redfish_json, csv, or photo")
	}
	if req.Input == nil {
		return ConfirmResult{}, fmt.Errorf("discover: confirm input is required")
	}
	// Reject if any sensitive keys are present at all (even [REDACTED] placeholders).
	// Learned few-shots must not carry secret-shaped fields.
	if hasSensitiveKeys(req.Input) {
		return ConfirmResult{}, fmt.Errorf("discover: confirm input must not include secret-shaped keys (strip password/token fields entirely)")
	}
	clean := redact.Map(req.Input)
	// Ensure result has no secrets in free-form confidences evidence is fine.
	if err := validate.NormalizationResult(req.Result); err != nil {
		return ConfirmResult{}, fmt.Errorf("discover: confirm result: %w", err)
	}
	// Operator accepted truth — clear needs_review for the stored output.
	out := req.Result
	out.NeedsReview = false

	ex := fewshot.Example{
		CreatedAt: time.Now().UTC(),
		Prompt:    fewshot.PromptReconcileAsset,
		Kind:      kind,
		Input:     clean,
		Partial:   req.Partial,
		Output:    out,
		Source:    "operator_confirm",
		DeviceID:  req.DeviceID,
	}
	stored, err := s.FewShot.Append(ctx, ex)
	if err != nil {
		return ConfirmResult{}, fmt.Errorf("discover: learn: %w", err)
	}
	if s.Log != nil {
		s.Log.Info("discover confirmed few-shot",
			"kind", kind,
			"serial", out.Asset.Serial,
			"device_id", req.DeviceID,
			"id", stored.ID,
		)
	}
	return ConfirmResult{
		Learned: true,
		ID:      stored.ID,
		Prompt:  fewshot.PromptReconcileAsset,
	}, nil
}

func hasSensitiveKeys(m map[string]any) bool {
	if m == nil {
		return false
	}
	for k, v := range m {
		if redact.IsSensitiveKey(k) {
			return true
		}
		switch t := v.(type) {
		case map[string]any:
			if hasSensitiveKeys(t) {
				return true
			}
		case []any:
			for _, el := range t {
				if nested, ok := el.(map[string]any); ok && hasSensitiveKeys(nested) {
					return true
				}
			}
		}
	}
	return false
}
