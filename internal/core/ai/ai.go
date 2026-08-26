// Package ai is the sole LLM transport boundary, with net/http clients for
// Ollama and OpenAI-compatible endpoints plus a Fake for tests.
// Discover/Observe/Deploy never call an LLM directly — they go through Core.
package ai

import (
	"context"
)

// LLM is the provider-agnostic completion interface.
// Phase 1 provides a Fake for unit tests; real Ollama/cloud clients land in Phase 3 / PR9.
type LLM interface {
	Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
	CompleteVision(ctx context.Context, req VisionRequest) (CompletionResponse, error)
}

// CompletionRequest is a text completion call.
type CompletionRequest struct {
	Model  string
	System string
	User   string
	// SchemaName selects a versioned schema blob under prompts/schemas/
	// (e.g. "normalization_result.v1") that is inlined into the prompt.
	SchemaName  string
	Temperature float64
	MaxTokens   int
}

// VisionRequest is a multimodal completion call.
type VisionRequest struct {
	CompletionRequest
	// ImageJPEG or ImagePNG bytes; max 4 MiB after compression for MVP.
	Image     []byte
	MediaType string // "image/jpeg" | "image/png"
}

// CompletionResponse is raw model text plus optional usage metadata.
type CompletionResponse struct {
	Content      string // raw model text (may include markdown fences)
	Model        string
	PromptTokens int
	OutputTokens int
	LatencyMS    int64
}
