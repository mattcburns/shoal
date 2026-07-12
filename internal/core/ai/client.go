package ai

import (
	"fmt"

	"github.com/mattcburns/shoal/internal/common/config"
)

// NewFromConfig selects an LLM implementation from app config (design §6).
// Returns an error when provider is set but required endpoints/models are missing.
// Empty provider returns (nil, nil) so composition roots can skip AI.
func NewFromConfig(cfg config.Config) (LLM, error) {
	switch cfg.AIProvider {
	case "":
		return nil, nil
	case "ollama":
		if cfg.OllamaURL == "" {
			return nil, fmt.Errorf("ai: SHOAL_OLLAMA_URL required when provider is ollama")
		}
		if cfg.AIModel == "" {
			return nil, fmt.Errorf("ai: SHOAL_AI_MODEL required when provider is ollama")
		}
		return NewOllama(cfg.OllamaURL, cfg.AIModel, cfg.AIVisionModel), nil
	case "cloud":
		if cfg.CloudAIBaseURL == "" {
			return nil, fmt.Errorf("ai: SHOAL_CLOUD_AI_BASE_URL required when provider is cloud")
		}
		if cfg.CloudAIAPIKey == "" {
			return nil, fmt.Errorf("ai: SHOAL_CLOUD_AI_API_KEY required when provider is cloud")
		}
		if cfg.AIModel == "" {
			return nil, fmt.Errorf("ai: SHOAL_AI_MODEL required when provider is cloud")
		}
		return NewOpenAICompat(cfg.CloudAIBaseURL, cfg.CloudAIAPIKey, cfg.AIModel, cfg.AIVisionModel), nil
	default:
		return nil, fmt.Errorf("ai: unknown SHOAL_AI_PROVIDER %q", cfg.AIProvider)
	}
}
