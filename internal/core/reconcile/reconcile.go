// Package reconcile implements AI-only asset/event reconciliation (Core).
// Core never imports Discover.
package reconcile

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/redact"
	"github.com/mattcburns/shoal/internal/common/validate"
	"github.com/mattcburns/shoal/internal/core/ai"
	"github.com/mattcburns/shoal/internal/core/ai/decode"
	"github.com/mattcburns/shoal/prompts"
)

// Reconciler is the AI-only reconciliation surface.
type Reconciler interface {
	ReconcileAsset(ctx context.Context, in ReconcileAssetInput) (models.NormalizationResult, error)
	ReconcileAssetPhoto(ctx context.Context, in ReconcilePhotoInput) (models.NormalizationResult, error)
	ReconcileEvent(ctx context.Context, in models.RawEventInput) (models.NormalizedEvent, error)
}

// ReconcileAssetInput is redacted raw + optional deterministic partial.
type ReconcileAssetInput struct {
	RedactedRaw    map[string]any
	Partial        *models.NormalizedAsset
	PartialSources []models.FieldConfidence
}

// ReconcilePhotoInput is a photo bytes payload for CompleteVision.
type ReconcilePhotoInput struct {
	Image     []byte
	MediaType string
	// Optional BMC IP hint (never a password).
	BMCIP string
}

// Service implements Reconciler.
type Service struct {
	LLM    ai.LLM
	Log    *slog.Logger
	Assets prompts.Assets
}

// New constructs a Service. assets may be empty; Load is attempted if needed.
func New(llm ai.LLM, log *slog.Logger) (*Service, error) {
	if log == nil {
		log = slog.Default()
	}
	assets, err := prompts.Load()
	if err != nil {
		return nil, err
	}
	return &Service{LLM: llm, Log: log, Assets: assets}, nil
}

// ReconcileAsset runs Complete + decode + validate.
func (s *Service) ReconcileAsset(ctx context.Context, in ReconcileAssetInput) (models.NormalizationResult, error) {
	if s.LLM == nil {
		return models.NormalizationResult{}, fmt.Errorf("reconcile: LLM not configured")
	}
	if in.RedactedRaw != nil && redact.ContainsSensitiveKey(in.RedactedRaw) {
		return models.NormalizationResult{}, fmt.Errorf("reconcile: redacted raw still contains sensitive keys")
	}
	rawJSON, err := json.Marshal(in.RedactedRaw)
	if err != nil {
		return models.NormalizationResult{}, err
	}
	partialJSON := "null"
	if in.Partial != nil {
		b, err := json.Marshal(in.Partial)
		if err != nil {
			return models.NormalizationResult{}, err
		}
		partialJSON = string(b)
	}
	user := ai.BuildReconcileAssetPrompt(
		s.Assets.ReconcileAssetMD,
		s.Assets.SchemaNormalizationResult,
		s.Assets.ReconcileAssetFewShot,
		string(rawJSON),
		partialJSON,
	)
	start := time.Now()
	resp, err := s.LLM.Complete(ctx, ai.CompletionRequest{
		System: "You output only valid JSON matching NormalizationResult.",
		User:   user,
	})
	if err != nil {
		return models.NormalizationResult{}, fmt.Errorf("reconcile: complete: %w", err)
	}
	s.Log.Info("ai complete",
		"model", resp.Model,
		"latency_ms", resp.LatencyMS,
		"prompt_tokens", resp.PromptTokens,
		"output_tokens", resp.OutputTokens,
		"elapsed_ms", time.Since(start).Milliseconds(),
		"kind", "reconcile_asset",
	)
	result, err := decode.DecodeJSON[models.NormalizationResult](resp.Content)
	if err != nil {
		return models.NormalizationResult{}, fmt.Errorf("reconcile: decode: %w", err)
	}
	// Force AI source labels when model omits them.
	for i := range result.Confidences {
		if result.Confidences[i].Source == "" {
			result.Confidences[i].Source = "ai"
		}
	}
	if err := validate.NormalizationResult(result); err != nil {
		return models.NormalizationResult{}, fmt.Errorf("reconcile: validate: %w", err)
	}
	return result, nil
}

// ReconcileAssetPhoto uses a two-step path:
//  1. CompleteVision — free-form description / OCR-ish text (tiny VLMs like moondream)
//  2. Complete (text model) — structure that description into NormalizationResult
//
// Direct schema JSON from small vision models is unreliable (truncated / looping garbage).
func (s *Service) ReconcileAssetPhoto(ctx context.Context, in ReconcilePhotoInput) (models.NormalizationResult, error) {
	if s.LLM == nil {
		return models.NormalizationResult{}, fmt.Errorf("reconcile: LLM not configured")
	}
	if len(in.Image) == 0 {
		return models.NormalizationResult{}, fmt.Errorf("reconcile: empty image")
	}
	media := in.MediaType
	if media == "" {
		media = "image/jpeg"
	}

	// Keep the vision prompt short and fixed: moondream often returns empty
	// content for long prompts, system messages, or even small prompt variants.
	// Do not append operator hints here — pass them to the text structure step.
	const describeUser = "List all readable text in this image."

	start := time.Now()
	vision, err := s.LLM.CompleteVision(ctx, ai.VisionRequest{
		CompletionRequest: ai.CompletionRequest{
			// System intentionally empty for VLMs that mishandle system+image.
			User: describeUser,
		},
		Image:     in.Image,
		MediaType: media,
	})
	if err != nil {
		return models.NormalizationResult{}, fmt.Errorf("reconcile: vision: %w", err)
	}
	s.Log.Info("ai complete_vision",
		"model", vision.Model,
		"latency_ms", vision.LatencyMS,
		"elapsed_ms", time.Since(start).Milliseconds(),
		"kind", "reconcile_asset_photo_describe",
		"image_bytes", len(in.Image),
		"desc_chars", len(vision.Content),
	)

	desc := strings.TrimSpace(vision.Content)
	if desc == "" {
		// Last resort: still attempt text structuring from operator hints so the
		// photo path can complete (always needs_review).
		if in.BMCIP == "" {
			return models.NormalizationResult{}, fmt.Errorf("reconcile: vision returned empty description")
		}
		desc = "No readable text extracted from photo. Operator BMC IP hint: " + in.BMCIP
		s.Log.Warn("vision empty description; using bmc_ip hint only")
	}
	// Cap description size before text reconcile (keep prompt bounded).
	if len(desc) > 4000 {
		desc = desc[:4000] + "…"
	}

	raw := map[string]any{
		"source":            "photo_description",
		"photo_description": desc,
		// Strongly prefer emitting serial/vendor/model when present in the description.
		"instruction": "If serial is not visible, set serial to photo-unknown and needs_review true. Always set bmc_ip from bmc_ip_hint when provided.",
	}
	if in.BMCIP != "" {
		raw["bmc_ip_hint"] = in.BMCIP
	}
	// Prefer structured text-model JSON; photo_description is never secret-bearing.
	result, err := s.ReconcileAsset(ctx, ReconcileAssetInput{RedactedRaw: raw})
	if err != nil {
		// Soft recovery: small models sometimes omit required fields.
		result, err = s.photoFallbackResult(desc, in.BMCIP)
		if err != nil {
			return models.NormalizationResult{}, fmt.Errorf("reconcile: photo structure: %w", err)
		}
	}
	result = completePhotoIdentity(result, desc, in.BMCIP)
	if err := validate.NormalizationResult(result); err != nil {
		result, err = s.photoFallbackResult(desc, in.BMCIP)
		if err != nil {
			return models.NormalizationResult{}, fmt.Errorf("reconcile: photo validate: %w", err)
		}
	}
	return result, nil
}

// completePhotoIdentity fills missing serial/bmc_ip from description/hints.
func completePhotoIdentity(r models.NormalizationResult, desc, bmcIP string) models.NormalizationResult {
	// Operator BMC IP is authoritative for photo ingest (model often ignores hints).
	if bmcIP != "" {
		r.Asset.BMCIP = bmcIP
	}
	if strings.TrimSpace(r.Asset.Serial) == "" {
		if s := extractSerialish(desc); s != "" {
			r.Asset.Serial = s
		} else {
			r.Asset.Serial = "photo-unknown"
		}
		r.NeedsReview = true
	}
	if strings.EqualFold(r.Asset.Serial, "unknown") || strings.HasPrefix(strings.ToLower(r.Asset.Serial), "unknown") {
		r.Asset.Serial = "photo-unknown"
		r.NeedsReview = true
	}
	// Ensure minimal confidences for required fields.
	has := map[string]bool{}
	for _, fc := range r.Confidences {
		has[strings.ToLower(fc.Field)] = true
	}
	if !has["serial"] {
		r.Confidences = append(r.Confidences, models.FieldConfidence{
			Field: "serial", Confidence: 0.3, Source: "ai", Evidence: "photo path",
		})
	}
	if !has["bmc_ip"] && r.Asset.BMCIP != "" {
		r.Confidences = append(r.Confidences, models.FieldConfidence{
			Field: "bmc_ip", Confidence: 0.9, Source: "ai", Evidence: "operator hint",
		})
	}
	r.NeedsReview = true // photo ingest always reviewable in MVP
	return r
}

func extractSerialish(desc string) string {
	// Prefer tokens that look like serials (contain digit + letter).
	fields := strings.Fields(desc)
	for _, f := range fields {
		f = strings.Trim(f, ".,;:\"'()[]")
		if len(f) < 4 || len(f) > 40 {
			continue
		}
		hasDigit, hasLetter := false, false
		for _, r := range f {
			if r >= '0' && r <= '9' {
				hasDigit = true
			}
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				hasLetter = true
			}
		}
		low := strings.ToLower(f)
		if hasDigit && hasLetter && !strings.Contains(low, "http") {
			return f
		}
	}
	return ""
}

func (s *Service) photoFallbackResult(desc, bmcIP string) (models.NormalizationResult, error) {
	serial := extractSerialish(desc)
	if serial == "" {
		serial = "photo-unknown"
	}
	if bmcIP == "" {
		bmcIP = "0.0.0.0"
	}
	r := models.NormalizationResult{
		Asset: models.NormalizedAsset{
			Serial: serial,
			BMCIP:  bmcIP,
			Vendor: "",
			Model:  "",
		},
		Confidences: []models.FieldConfidence{
			{Field: "serial", Confidence: 0.25, Source: "ai", Evidence: "photo fallback"},
			{Field: "bmc_ip", Confidence: 0.9, Source: "ai", Evidence: "operator hint or placeholder"},
		},
		NeedsReview: true,
	}
	if err := validate.NormalizationResult(r); err != nil {
		return models.NormalizationResult{}, err
	}
	return r, nil
}

// ReconcileEvent is a minimal text event normalizer for later Observe use.
func (s *Service) ReconcileEvent(ctx context.Context, in models.RawEventInput) (models.NormalizedEvent, error) {
	if in.Raw != nil && redact.ContainsSensitiveKey(in.Raw) {
		return models.NormalizedEvent{}, fmt.Errorf("reconcile: event raw still contains sensitive keys")
	}
	// Phase 3: deterministic pass-through; AI event path expands in Phase 4.
	ev := models.NormalizedEvent{
		DeviceID:  in.DeviceID,
		EventType: in.Source,
		Severity:  "info",
		Message:   in.Message,
		Timestamp: in.Timestamp,
	}
	if err := validate.NormalizedEvent(ev); err != nil {
		return models.NormalizedEvent{}, err
	}
	return ev, nil
}

var _ Reconciler = (*Service)(nil)
