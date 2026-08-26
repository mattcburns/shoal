package profile

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/redact"
	"github.com/mattcburns/shoal/internal/core/ai"
	"github.com/mattcburns/shoal/internal/core/ai/decode"
	"github.com/mattcburns/shoal/prompts"
)

// Profiler is the Core profile generation surface (AI only; no Redfish/NetBox).
type Profiler interface {
	GenerateProvisioningProfile(
		ctx context.Context,
		asset models.NormalizedAsset,
		requirements models.ProfileRequirements,
	) (models.ProvisioningProfile, error)
}

// Service implements Profiler.
type Service struct {
	LLM    ai.LLM
	Log    *slog.Logger
	Assets prompts.Assets
}

// New constructs a profile Service.
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

// GenerateProvisioningProfile runs Complete + decode + validate.
func (s *Service) GenerateProvisioningProfile(
	ctx context.Context,
	asset models.NormalizedAsset,
	requirements models.ProfileRequirements,
) (models.ProvisioningProfile, error) {
	if s.LLM == nil {
		return models.ProvisioningProfile{}, fmt.Errorf("profile: LLM not configured")
	}
	if err := ProfileRequirements(requirements); err != nil {
		return models.ProvisioningProfile{}, err
	}
	// Redact extra map defensively even after validate.
	if requirements.Extra != nil {
		clean := make(map[string]string, len(requirements.Extra))
		for k, v := range requirements.Extra {
			if redact.IsSensitiveKey(k) {
				continue
			}
			clean[k] = v
		}
		requirements.Extra = clean
	}

	assetJSON, err := json.Marshal(asset)
	if err != nil {
		return models.ProvisioningProfile{}, err
	}
	reqJSON, err := json.Marshal(requirements)
	if err != nil {
		return models.ProvisioningProfile{}, err
	}
	user := ai.BuildProvisioningProfilePrompt(
		s.Assets.ProvisioningProfileMD,
		s.Assets.SchemaProvisioningProfile,
		s.Assets.ProvisioningProfileFewShot,
		string(assetJSON),
		string(reqJSON),
	)
	start := time.Now()
	resp, err := s.LLM.Complete(ctx, ai.CompletionRequest{
		System: "You output only valid JSON matching ProvisioningProfile.",
		User:   user,
	})
	if err != nil {
		return models.ProvisioningProfile{}, fmt.Errorf("profile: complete: %w", err)
	}
	s.Log.Info("ai complete",
		"model", resp.Model,
		"latency_ms", resp.LatencyMS,
		"prompt_tokens", resp.PromptTokens,
		"output_tokens", resp.OutputTokens,
		"elapsed_ms", time.Since(start).Milliseconds(),
		"kind", "provisioning_profile",
	)
	raw := decode.StripCodeFences(resp.Content)
	obj, err := decode.ExtractJSONObject(raw)
	if err != nil {
		return models.ProvisioningProfile{}, fmt.Errorf("profile: extract json: %w", err)
	}
	var out models.ProvisioningProfile
	if err := json.Unmarshal([]byte(obj), &out); err != nil {
		return models.ProvisioningProfile{}, fmt.Errorf("profile: decode: %w", err)
	}
	// Force approval when destruct steps present.
	if len(out.DestructSteps) > 0 {
		out.NeedsApproval = true
	}
	if !requirements.AllowDestruct {
		out.DestructSteps = nil
		// keep NeedsApproval if model set it for review, else clear when no destruct
		if len(out.DestructSteps) == 0 && !out.NeedsApproval {
			out.NeedsApproval = false
		}
	}
	if err := ProvisioningProfile(out); err != nil {
		return models.ProvisioningProfile{}, err
	}
	return out, nil
}

var _ Profiler = (*Service)(nil)
