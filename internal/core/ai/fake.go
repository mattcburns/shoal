package ai

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Fake is an in-memory LLM for unit tests. It does not call the network.
type Fake struct {
	mu       sync.Mutex
	Calls    []CompletionRequest
	VisCalls []VisionRequest
	// Content is returned for Complete when ResponseFn is nil.
	Content string
	// ResponseFn overrides Content when set.
	ResponseFn func(req CompletionRequest) (CompletionResponse, error)
	// VisionFn overrides vision responses when set.
	VisionFn func(req VisionRequest) (CompletionResponse, error)
	// Err if set is returned from Complete/CompleteVision (unless ResponseFn/VisionFn set).
	Err error
}

// Complete records the request and returns canned content.
func (f *Fake) Complete(_ context.Context, req CompletionRequest) (CompletionResponse, error) {
	f.mu.Lock()
	f.Calls = append(f.Calls, req)
	f.mu.Unlock()

	if f.ResponseFn != nil {
		return f.ResponseFn(req)
	}
	if f.Err != nil {
		return CompletionResponse{}, f.Err
	}
	content := f.Content
	if content == "" {
		content = `{"ok":true}`
	}
	return CompletionResponse{
		Content:   content,
		Model:     req.Model,
		LatencyMS: 1,
	}, nil
}

// CompleteVision records the vision request and returns canned content.
func (f *Fake) CompleteVision(_ context.Context, req VisionRequest) (CompletionResponse, error) {
	f.mu.Lock()
	f.VisCalls = append(f.VisCalls, req)
	f.mu.Unlock()

	if f.VisionFn != nil {
		return f.VisionFn(req)
	}
	if f.Err != nil {
		return CompletionResponse{}, f.Err
	}
	if len(req.Image) == 0 {
		return CompletionResponse{}, fmt.Errorf("ai/fake: empty image")
	}
	content := f.Content
	if content == "" {
		content = `{"ok":true,"vision":true}`
	}
	return CompletionResponse{
		Content:   content,
		Model:     req.Model,
		LatencyMS: 1,
	}, nil
}

// Reset clears recorded calls.
func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = nil
	f.VisCalls = nil
}

// Ensure Fake implements LLM at compile time.
var _ LLM = (*Fake)(nil)

// SleepingFake wraps Fake with artificial latency (optional tests).
type SleepingFake struct {
	Fake
	Delay time.Duration
}

// Complete delays then delegates.
func (s *SleepingFake) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	if s.Delay > 0 {
		select {
		case <-ctx.Done():
			return CompletionResponse{}, ctx.Err()
		case <-time.After(s.Delay):
		}
	}
	return s.Fake.Complete(ctx, req)
}
