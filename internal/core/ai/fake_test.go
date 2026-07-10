package ai_test

import (
	"context"
	"testing"

	"github.com/mattcburns/shoal/internal/core/ai"
)

func TestFakeSatisfiesLLM(t *testing.T) {
	var _ ai.LLM = (*ai.Fake)(nil)
	f := &ai.Fake{Content: `{"asset":{"serial":"x"}}`}
	resp, err := f.Complete(context.Background(), ai.CompletionRequest{
		Model:  "test",
		System: "sys",
		User:   "user",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content == "" {
		t.Fatal("empty content")
	}
	if len(f.Calls) != 1 {
		t.Fatalf("calls=%d", len(f.Calls))
	}
}

func TestFakeVision(t *testing.T) {
	f := &ai.Fake{}
	_, err := f.CompleteVision(context.Background(), ai.VisionRequest{
		CompletionRequest: ai.CompletionRequest{Model: "v"},
		Image:             []byte{0xff, 0xd8},
		MediaType:         "image/jpeg",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.VisCalls) != 1 {
		t.Fatalf("vis calls=%d", len(f.VisCalls))
	}
}

func TestFakeError(t *testing.T) {
	f := &ai.Fake{Err: context.Canceled}
	_, err := f.Complete(context.Background(), ai.CompletionRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
}
