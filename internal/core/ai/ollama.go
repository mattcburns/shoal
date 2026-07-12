package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Ollama is an LLM client for a local Ollama server (design §6).
type Ollama struct {
	BaseURL     string
	TextModel   string
	VisionModel string
	HTTP        *http.Client
}

// NewOllama constructs a client. baseURL is e.g. http://192.168.122.100:11434.
func NewOllama(baseURL, textModel, visionModel string) *Ollama {
	return &Ollama{
		BaseURL:     strings.TrimRight(baseURL, "/"),
		TextModel:   textModel,
		VisionModel: visionModel,
		HTTP:        &http.Client{Timeout: 5 * time.Minute},
	}
}

type ollamaChatRequest struct {
	Model    string              `json:"model"`
	Messages []ollamaChatMessage `json:"messages"`
	Stream   bool                `json:"stream"`
	// Format hints JSON mode when supported.
	Format string `json:"format,omitempty"`
}

type ollamaChatMessage struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"` // base64 without data: prefix
}

type ollamaChatResponse struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Model string `json:"model"`
	// PromptEvalCount / EvalCount when present.
	PromptEvalCount int `json:"prompt_eval_count"`
	EvalCount       int `json:"eval_count"`
}

// Complete implements LLM (text path → SHOAL_AI_MODEL).
func (o *Ollama) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	model := req.Model
	if model == "" {
		model = o.TextModel
	}
	if model == "" {
		return CompletionResponse{}, fmt.Errorf("ai/ollama: no text model configured")
	}
	return o.chat(ctx, model, req.System, req.User, nil)
}

// CompleteVision implements LLM (vision model preferred).
func (o *Ollama) CompleteVision(ctx context.Context, req VisionRequest) (CompletionResponse, error) {
	if len(req.Image) == 0 {
		return CompletionResponse{}, fmt.Errorf("ai/ollama: empty image")
	}
	model := req.Model
	if model == "" {
		model = o.VisionModel
	}
	if model == "" {
		model = o.TextModel
	}
	if model == "" {
		return CompletionResponse{}, fmt.Errorf("ai/ollama: no vision model configured (set SHOAL_AI_VISION_MODEL)")
	}
	// llama3.2:3b is text-only; require an explicit vision model when TextModel would be used by mistake.
	if o.VisionModel == "" && model == o.TextModel {
		return CompletionResponse{}, fmt.Errorf("ai/ollama: photo path requires SHOAL_AI_VISION_MODEL (text model %q cannot accept images)", model)
	}
	b64 := base64.StdEncoding.EncodeToString(req.Image)
	return o.chat(ctx, model, req.System, req.User, []string{b64})
}

func (o *Ollama) chat(ctx context.Context, model, system, user string, images []string) (CompletionResponse, error) {
	if o.BaseURL == "" {
		return CompletionResponse{}, fmt.Errorf("ai/ollama: empty base URL")
	}
	msgs := make([]ollamaChatMessage, 0, 2)
	if system != "" {
		msgs = append(msgs, ollamaChatMessage{Role: "system", Content: system})
	}
	um := ollamaChatMessage{Role: "user", Content: user}
	if len(images) > 0 {
		um.Images = images
	}
	msgs = append(msgs, um)

	body, err := json.Marshal(ollamaChatRequest{
		Model:    model,
		Messages: msgs,
		Stream:   false,
		Format:   "json",
	})
	if err != nil {
		return CompletionResponse{}, err
	}

	start := time.Now()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return CompletionResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := o.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("ai/ollama: chat: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return CompletionResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return CompletionResponse{}, fmt.Errorf("ai/ollama: status %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var parsed ollamaChatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return CompletionResponse{}, fmt.Errorf("ai/ollama: decode response: %w", err)
	}
	return CompletionResponse{
		Content:      parsed.Message.Content,
		Model:        firstNonEmpty(parsed.Model, model),
		PromptTokens: parsed.PromptEvalCount,
		OutputTokens: parsed.EvalCount,
		LatencyMS:    time.Since(start).Milliseconds(),
	}, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

var _ LLM = (*Ollama)(nil)
