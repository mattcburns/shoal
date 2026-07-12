// Package reconcile implements AI-only asset/event reconciliation (Core).
// Core never imports Discover.
package reconcile

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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

// ReconcileAssetPhoto uses CompleteVision.
func (s *Service) ReconcileAssetPhoto(ctx context.Context, in ReconcilePhotoInput) (models.NormalizationResult, error) {
	if s.LLM == nil {
		return models.NormalizationResult{}, fmt.Errorf("reconcile: LLM not configured")
	}
	if len(in.Image) == 0 {
		return models.NormalizationResult{}, fmt.Errorf("reconcile: empty image")
	}
	hint := map[string]any{"note": "extract serial, vendor, model, bmc_ip from the photo if visible"}
	if in.BMCIP != "" {
		hint["bmc_ip_hint"] = in.BMCIP
	}
	rawJSON, _ := json.Marshal(hint)
	user := ai.BuildReconcileAssetPrompt(
		s.Assets.ReconcileAssetMD,
		s.Assets.SchemaNormalizationResult,
		s.Assets.ReconcileAssetFewShot,
		string(rawJSON),
		"null",
	)
	media := in.MediaType
	if media == "" {
		media = "image/jpeg"
	}
	start := time.Now()
	resp, err := s.LLM.CompleteVision(ctx, ai.VisionRequest{
		CompletionRequest: ai.CompletionRequest{
			System: "You output only valid JSON matching NormalizationResult from the image.",
			User:   user,
		},
		Image:     in.Image,
		MediaType: media,
	})
	if err != nil {
		return models.NormalizationResult{}, fmt.Errorf("reconcile: vision: %w", err)
	}
	s.Log.Info("ai complete_vision",
		"model", resp.Model,
		"latency_ms", resp.LatencyMS,
		"elapsed_ms", time.Since(start).Milliseconds(),
		"kind", "reconcile_asset_photo",
		"image_bytes", len(in.Image),
	)
	result, err := decode.DecodeJSON[models.NormalizationResult](resp.Content)
	if err != nil {
		return models.NormalizationResult{}, fmt.Errorf("reconcile: decode: %w", err)
	}
	for i := range result.Confidences {
		if result.Confidences[i].Source == "" {
			result.Confidences[i].Source = "ai"
		}
	}
	if in.BMCIP != "" && result.Asset.BMCIP == "" {
		result.Asset.BMCIP = in.BMCIP
	}
	if err := validate.NormalizationResult(result); err != nil {
		return models.NormalizationResult{}, fmt.Errorf("reconcile: validate: %w", err)
	}
	return result, nil
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
