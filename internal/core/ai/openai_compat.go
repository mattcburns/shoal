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

// OpenAICompat is an OpenAI-compatible chat completions client (cloud path).
type OpenAICompat struct {
	BaseURL     string
	APIKey      string
	TextModel   string
	VisionModel string
	HTTP        *http.Client
}

// NewOpenAICompat constructs a cloud client. baseURL should include /v1 if required by the provider.
func NewOpenAICompat(baseURL, apiKey, textModel, visionModel string) *OpenAICompat {
	return &OpenAICompat{
		BaseURL:     strings.TrimRight(baseURL, "/"),
		APIKey:      apiKey,
		TextModel:   textModel,
		VisionModel: visionModel,
		HTTP:        &http.Client{Timeout: 5 * time.Minute},
	}
}

type oaiChatRequest struct {
	Model    string       `json:"model"`
	Messages []oaiMessage `json:"messages"`
}

type oaiMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []contentPart
}

type oaiContentPart struct {
	Type     string       `json:"type"`
	Text     string       `json:"text,omitempty"`
	ImageURL *oaiImageURL `json:"image_url,omitempty"`
}

type oaiImageURL struct {
	URL string `json:"url"`
}

type oaiChatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// Complete implements LLM.
func (c *OpenAICompat) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	model := req.Model
	if model == "" {
		model = c.TextModel
	}
	if model == "" {
		return CompletionResponse{}, fmt.Errorf("ai/cloud: no text model configured")
	}
	msgs := make([]oaiMessage, 0, 2)
	if req.System != "" {
		msgs = append(msgs, oaiMessage{Role: "system", Content: req.System})
	}
	msgs = append(msgs, oaiMessage{Role: "user", Content: req.User})
	return c.chat(ctx, model, msgs)
}

// CompleteVision implements LLM with image_url content parts.
func (c *OpenAICompat) CompleteVision(ctx context.Context, req VisionRequest) (CompletionResponse, error) {
	if len(req.Image) == 0 {
		return CompletionResponse{}, fmt.Errorf("ai/cloud: empty image")
	}
	model := req.Model
	if model == "" {
		model = c.VisionModel
	}
	if model == "" {
		model = c.TextModel
	}
	if model == "" {
		return CompletionResponse{}, fmt.Errorf("ai/cloud: no vision model configured")
	}
	media := req.MediaType
	if media == "" {
		media = "image/jpeg"
	}
	dataURL := "data:" + media + ";base64," + base64.StdEncoding.EncodeToString(req.Image)
	msgs := make([]oaiMessage, 0, 2)
	if req.System != "" {
		msgs = append(msgs, oaiMessage{Role: "system", Content: req.System})
	}
	parts := []oaiContentPart{
		{Type: "text", Text: req.User},
		{Type: "image_url", ImageURL: &oaiImageURL{URL: dataURL}},
	}
	msgs = append(msgs, oaiMessage{Role: "user", Content: parts})
	return c.chat(ctx, model, msgs)
}

func (c *OpenAICompat) chat(ctx context.Context, model string, msgs []oaiMessage) (CompletionResponse, error) {
	if c.BaseURL == "" {
		return CompletionResponse{}, fmt.Errorf("ai/cloud: empty base URL")
	}
	if c.APIKey == "" {
		return CompletionResponse{}, fmt.Errorf("ai/cloud: empty API key")
	}
	body, err := json.Marshal(oaiChatRequest{Model: model, Messages: msgs})
	if err != nil {
		return CompletionResponse{}, err
	}
	url := c.BaseURL + "/chat/completions"
	start := time.Now()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return CompletionResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("ai/cloud: chat: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return CompletionResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return CompletionResponse{}, fmt.Errorf("ai/cloud: status %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var parsed oaiChatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return CompletionResponse{}, fmt.Errorf("ai/cloud: decode: %w", err)
	}
	content := ""
	if len(parsed.Choices) > 0 {
		content = parsed.Choices[0].Message.Content
	}
	return CompletionResponse{
		Content:      content,
		Model:        firstNonEmpty(parsed.Model, model),
		PromptTokens: parsed.Usage.PromptTokens,
		OutputTokens: parsed.Usage.CompletionTokens,
		LatencyMS:    time.Since(start).Milliseconds(),
	}, nil
}

var _ LLM = (*OpenAICompat)(nil)
