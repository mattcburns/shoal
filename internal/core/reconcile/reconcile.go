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

// ReconcileAssetPhoto runs vision OCR then builds identity from the OCR text.
//
// Preferred lab model: deepseek-ocr (SHOAL_AI_VISION_MODEL) with prompt "Free OCR.".
// We parse labeled fields (SERIAL/VENDOR/MODEL) from the OCR output. If serial
// cannot be extracted, the call fails — we do not invent photo-unknown placeholders.
func (s *Service) ReconcileAssetPhoto(ctx context.Context, in ReconcilePhotoInput) (models.NormalizationResult, error) {
	if s.LLM == nil {
		return models.NormalizationResult{}, fmt.Errorf("reconcile: LLM not configured")
	}
	if len(in.Image) == 0 {
		return models.NormalizationResult{}, fmt.Errorf("reconcile: empty image")
	}
	if strings.TrimSpace(in.BMCIP) == "" {
		return models.NormalizationResult{}, fmt.Errorf("reconcile: photo: bmc_ip is required (-bmc-ip)")
	}
	media := in.MediaType
	if media == "" {
		media = "image/jpeg"
	}

	start := time.Now()
	vision, err := s.LLM.CompleteVision(ctx, ai.VisionRequest{
		CompletionRequest: ai.CompletionRequest{
			// System empty: OCR VLMs work best with a short user prompt only.
			User: photoOCRPrompt,
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
		"kind", "reconcile_asset_photo_ocr",
		"image_bytes", len(in.Image),
		"ocr_chars", len(vision.Content),
	)

	desc := strings.TrimSpace(vision.Content)
	if desc == "" {
		return models.NormalizationResult{}, fmt.Errorf("reconcile: photo: vision OCR returned empty text (model=%s); try deepseek-ocr", vision.Model)
	}
	if len(desc) > 4000 {
		desc = desc[:4000] + "…"
	}

	ocr := parseOCRIdentity(desc)
	// Primary path: structured OCR labels (deepseek-ocr "Free OCR." output).
	if ocr.Serial != "" {
		result, err := resultFromOCR(ocr, in.BMCIP)
		if err != nil {
			return models.NormalizationResult{}, err
		}
		if err := validate.NormalizationResult(result); err != nil {
			return models.NormalizationResult{}, fmt.Errorf("reconcile: photo validate: %w", err)
		}
		s.Log.Info("photo ocr identity",
			"serial", result.Asset.Serial,
			"vendor", result.Asset.Vendor,
			"model", result.Asset.Model,
			"needs_review", result.NeedsReview,
		)
		return result, nil
	}

	// Secondary: text model structures free-form caption (weaker VLMs).
	// Still require a real serial after structure — no synthetic IDs.
	raw := map[string]any{
		"source":            "photo_ocr",
		"photo_description": desc,
		"instruction":       "Extract serial, vendor, model from photo_description. bmc_ip must be " + in.BMCIP + ". serial is required.",
		"bmc_ip_hint":       in.BMCIP,
	}
	result, err := s.ReconcileAsset(ctx, ReconcileAssetInput{RedactedRaw: raw})
	if err != nil {
		return models.NormalizationResult{}, fmt.Errorf("reconcile: photo structure: %w; ocr_text=%q", err, truncateRunes(desc, 160))
	}
	if in.BMCIP != "" {
		result.Asset.BMCIP = in.BMCIP
	}
	// Prefer any OCR-parsed vendor/model if LLM left them empty.
	if result.Asset.Vendor == "" {
		result.Asset.Vendor = ocr.Vendor
	}
	if result.Asset.Model == "" {
		result.Asset.Model = ocr.Model
	}
	if strings.TrimSpace(result.Asset.Serial) == "" ||
		strings.EqualFold(result.Asset.Serial, "unknown") ||
		strings.HasPrefix(strings.ToLower(result.Asset.Serial), "unknown") ||
		strings.HasPrefix(strings.ToLower(result.Asset.Serial), "photo-unknown") {
		return models.NormalizationResult{}, errNoSerialFromPhoto(desc)
	}
	if err := validate.NormalizationResult(result); err != nil {
		return models.NormalizationResult{}, fmt.Errorf("reconcile: photo validate: %w", err)
	}
	result.NeedsReview = true
	return result, nil
}

func truncateRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
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
