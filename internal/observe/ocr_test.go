package observe_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/common/telemetry"
	"github.com/mattcburns/shoal/internal/core/ai"
	"github.com/mattcburns/shoal/internal/core/ocr"
	"github.com/mattcburns/shoal/internal/observe"
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

func TestOCRFailureScreenFilePersistsEvent(t *testing.T) {
	fake := &ai.Fake{Content: `{
  "raw_text": "No bootable device",
  "summary": "Boot media missing",
  "category": "boot_error",
  "confidence": 0.9,
  "evidence": "No bootable device"
}`}
	ocrSvc, err := ocr.New(fake, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := telemetry.NewMemory()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := observe.New(log, nil, store, nil)
	out, err := svc.OCRFailureScreen(context.Background(), ocrSvc, observe.OCRInput{
		DeviceID:  "dev-ocr-1",
		Image:     tinyPNG,
		MediaType: "image/png",
		Persist:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Result.Category != "boot_error" || out.Source != "file" {
		t.Fatalf("%+v", out)
	}
	if out.EventID == "" {
		t.Fatal("expected event id")
	}
	evs, err := store.ListEvents(context.Background(), "dev-ocr-1", time.Time{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].EventType != "graphics_ocr" {
		t.Fatalf("%+v", evs)
	}
}

func TestOCRFailureScreenRedfishCaptureDebug(t *testing.T) {
	f := redfish.NewFake()
	f.ScreenshotErr = fmt.Errorf("screenshot unsupported")

	ocrSvc, err := ocr.New(&ai.Fake{Content: `{"raw_text":"x","summary":"x","category":"unknown","confidence":0.1}`}, nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := observe.New(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, telemetry.NewMemory(), nil)
	out, err := svc.OCRFailureScreen(context.Background(), ocrSvc, observe.OCRInput{
		DeviceID: "d1",
		BMC:      f,
		Persist:  false,
	})
	if err == nil {
		t.Fatal("expected capture error")
	}
	if out.Capture == nil || len(out.Capture.Debug) == 0 {
		t.Fatalf("expected capture debug on failure, got %+v", out.Capture)
	}
}
