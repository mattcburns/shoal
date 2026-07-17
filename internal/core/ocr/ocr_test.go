package ocr_test

import (
	"context"
	"testing"

	"github.com/mattcburns/shoal/internal/core/ai"
	"github.com/mattcburns/shoal/internal/core/ocr"
)

// Minimal 1x1 PNG.
var tinyPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xde, 0x00, 0x00, 0x00,
	0x0c, 0x49, 0x44, 0x41, 0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
	0x00, 0x00, 0x03, 0x00, 0x01, 0x00, 0x05, 0xfe, 0xd4, 0xef, 0x00, 0x00,
	0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

func TestAnalyzeFailureScreen(t *testing.T) {
	fake := &ai.Fake{Content: `{
  "raw_text": "No bootable device",
  "summary": "Boot media missing",
  "category": "boot_error",
  "confidence": 0.9,
  "evidence": "No bootable device"
}`}
	svc, err := ocr.New(fake, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := svc.AnalyzeFailureScreen(context.Background(), tinyPNG, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if r.Category != "boot_error" || r.RawText == "" {
		t.Fatalf("%+v", r)
	}
	if len(fake.VisCalls) != 1 || len(fake.VisCalls[0].Image) == 0 {
		t.Fatal("expected vision call with image")
	}
}

func TestAnalyzeRejectsHugeImage(t *testing.T) {
	svc, err := ocr.New(&ai.Fake{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	big := make([]byte, ocr.MaxImageBytes+1)
	copy(big, tinyPNG)
	_, err = svc.AnalyzeFailureScreen(context.Background(), big, "image/png")
	if err == nil {
		t.Fatal("expected size error")
	}
}

func TestValidateResult(t *testing.T) {
	if err := ocr.ValidateResult(ocr.Result{Category: "nope", Confidence: 0.5, RawText: "x"}); err == nil {
		t.Fatal("expected category error")
	}
}

func TestHeuristicFromFreeText(t *testing.T) {
	// Simulate Free-OCR model that returns plain text (no JSON).
	fake := &ai.Fake{
		VisionFn: func(req ai.VisionRequest) (ai.CompletionResponse, error) {
			return ai.CompletionResponse{Content: "No bootable device\nInsert boot media", Model: "deepseek-ocr"}, nil
		},
		ResponseFn: func(req ai.CompletionRequest) (ai.CompletionResponse, error) {
			// Force structure pass to fail → heuristic
			return ai.CompletionResponse{Content: "not json"}, nil
		},
	}
	svc, err := ocr.New(fake, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := svc.AnalyzeFailureScreen(context.Background(), tinyPNG, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if r.Category != "boot_error" {
		t.Fatalf("want boot_error got %+v", r)
	}
}
