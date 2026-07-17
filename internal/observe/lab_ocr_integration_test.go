//go:build integration

package observe_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/common/config"
	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/common/telemetry"
	"github.com/mattcburns/shoal/internal/core/ai"
	"github.com/mattcburns/shoal/internal/core/ocr"
	"github.com/mattcburns/shoal/internal/observe"
)

// TestLabOCRFailureScreenFile runs CompleteVision against lab Ollama on a fixture PNG.
// Requires SHOAL_AI_PROVIDER=ollama, SHOAL_OLLAMA_URL, SHOAL_AI_VISION_MODEL.
// Optional SHOAL_TELEMETRY_DATABASE_URL for event persist.
func TestLabOCRFailureScreenFile(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AIProvider == "" || cfg.OllamaURL == "" {
		t.Skip("set SHOAL_AI_PROVIDER=ollama and SHOAL_OLLAMA_URL for lab OCR")
	}
	if cfg.AIVisionModel == "" {
		t.Skip("set SHOAL_AI_VISION_MODEL for lab vision OCR")
	}

	imgPath := filepath.Join("..", "..", "testdata", "ocr", "failure_screen.png")
	if _, err := os.Stat(imgPath); err != nil {
		// when cwd is package dir
		imgPath = filepath.Join("testdata", "ocr", "failure_screen.png")
		if _, err := os.Stat(imgPath); err != nil {
			// from module root via test
			wd, _ := os.Getwd()
			t.Fatalf("fixture not found from %s: %v", wd, err)
		}
	}
	// Prefer finding from module root walking up
	for _, p := range []string{
		"testdata/ocr/failure_screen.png",
		"../testdata/ocr/failure_screen.png",
		"../../testdata/ocr/failure_screen.png",
		"../../../testdata/ocr/failure_screen.png",
	} {
		if _, err := os.Stat(p); err == nil {
			imgPath = p
			break
		}
	}
	img, err := os.ReadFile(imgPath)
	if err != nil {
		t.Fatal(err)
	}

	llm, err := ai.NewFromConfig(cfg)
	if err != nil || llm == nil {
		t.Fatalf("ai: %v", err)
	}
	ocrSvc, err := ocr.New(llm, nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	var store telemetry.Store
	if cfg.TelemetryDatabaseURL != "" {
		db, err := telemetry.OpenAndMigrate(ctx, cfg.TelemetryDatabaseURL)
		if err != nil {
			t.Fatalf("telemetry: %v", err)
		}
		defer db.Close()
		store = telemetry.NewPostgres(db)
	} else {
		store = telemetry.NewMemory()
	}

	svc := observe.New(nil, nil, store, nil)
	out, err := svc.OCRFailureScreen(ctx, ocrSvc, observe.OCRInput{
		DeviceID:  "lab-ocr-smoke",
		Image:     img,
		MediaType: "image/png",
		Persist:   true,
	})
	if err != nil {
		t.Fatalf("ocr: %v", err)
	}
	if out.Result.RawText == "" && out.Result.Summary == "" {
		t.Fatalf("empty OCR result: %+v", out.Result)
	}
	t.Logf("ocr category=%s confidence=%.2f summary=%q model=%s source=%s",
		out.Result.Category, out.Result.Confidence, out.Result.Summary, out.Result.Model, out.Source)
}

// TestLabOCRRedfishCaptureSushy expects unsupported capture with debug steps (sushy has no screenshot).
func TestLabOCRRedfishCaptureSushy(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	bmcURL := os.Getenv("SHOAL_BMC_URL")
	if bmcURL == "" {
		bmcURL = "http://192.168.122.100:8001"
	}
	user := cfg.BMCUsername
	if user == "" {
		user = "admin"
	}
	pass := cfg.BMCPassword
	if pass == "" {
		pass = "password"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bmc, err := redfish.NewBMC(redfish.Config{
		BaseURL: bmcURL, Username: user, Password: pass,
		AuthMode: "basic", TLSMode: "off",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := bmc.Open(ctx); err != nil {
		t.Skipf("bmc open: %v", err)
	}
	defer bmc.Close(context.Background())

	shot, err := bmc.CaptureScreenshot(ctx, "", redfish.ScreenshotCurrent)
	if err == nil {
		t.Fatalf("expected sushy capture to fail, got %d bytes vendor=%s", len(shot.Image), shot.Vendor)
	}
	if len(shot.Debug) == 0 {
		t.Fatal("expected debug steps on capture failure")
	}
	for i, s := range shot.Debug {
		t.Logf("debug[%d] phase=%s vendor=%s ok=%v status=%d msg=%s",
			i, s.Phase, s.Vendor, s.OK, s.StatusCode, s.Message)
	}
}
