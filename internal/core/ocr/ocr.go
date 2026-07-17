// Package ocr implements graphics failure-screen OCR via Core AI CompleteVision.
// Distinct from Discover asset-label Free OCR (SERIAL/VENDOR/MODEL).
package ocr

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/mattcburns/shoal/internal/core/ai"
	"github.com/mattcburns/shoal/internal/core/ai/decode"
	"github.com/mattcburns/shoal/prompts"
)

// MaxImageBytes is the decoded image size cap (same as Discover photo).
const MaxImageBytes = 4 << 20

// Result is structured failure-screen OCR output.
type Result struct {
	RawText    string  `json:"raw_text"`
	Summary    string  `json:"summary"`
	Category   string  `json:"category"`
	Confidence float64 `json:"confidence"`
	Evidence   string  `json:"evidence,omitempty"`
	// Model is the vision model that produced the result (for operators).
	Model string `json:"model,omitempty"`
	// LatencyMS is model call latency.
	LatencyMS int64 `json:"latency_ms,omitempty"`
}

// Service runs CompleteVision + decode/validate for failure screens.
type Service struct {
	LLM    ai.LLM
	Log    *slog.Logger
	Assets prompts.Assets
}

// New constructs an OCR Service.
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

// AnalyzeFailureScreen OCRs image bytes (jpeg/png). Does not log image contents.
func (s *Service) AnalyzeFailureScreen(ctx context.Context, image []byte, mediaType string) (Result, error) {
	if s.LLM == nil {
		return Result{}, fmt.Errorf("ocr: LLM not configured")
	}
	if len(image) == 0 {
		return Result{}, fmt.Errorf("ocr: empty image")
	}
	if len(image) > MaxImageBytes {
		return Result{}, fmt.Errorf("ocr: image exceeds %d bytes", MaxImageBytes)
	}
	mt := strings.ToLower(strings.TrimSpace(mediaType))
	if mt == "" {
		mt = sniffMediaType(image)
	}
	if mt != "image/jpeg" && mt != "image/png" {
		return Result{}, fmt.Errorf("ocr: unsupported media type %q (want image/jpeg or image/png)", mt)
	}

	// Step 1: Free OCR only (matches Discover photo / deepseek-ocr best practice).
	// Do not dump the full schema into the vision prompt — OCR models regurgitate it.
	start := time.Now()
	resp, err := s.LLM.CompleteVision(ctx, ai.VisionRequest{
		CompletionRequest: ai.CompletionRequest{
			System: "You perform OCR on failure screens. Output only the text visible in the image.",
			User:   "Free OCR.",
		},
		Image:     image,
		MediaType: mt,
	})
	if err != nil {
		return Result{}, fmt.Errorf("ocr: complete vision: %w", err)
	}
	s.Log.Info("ai complete vision",
		"model", resp.Model,
		"latency_ms", resp.LatencyMS,
		"prompt_tokens", resp.PromptTokens,
		"output_tokens", resp.OutputTokens,
		"elapsed_ms", time.Since(start).Milliseconds(),
		"kind", "failure_screen_ocr_free",
		"image_bytes", len(image),
		"media_type", mt,
	)

	// Prefer structured JSON if the model already emitted it.
	if out, ok := parseOCRContent(resp.Content); ok {
		out.Model = resp.Model
		out.LatencyMS = resp.LatencyMS
		return out, nil
	}

	text := cleanOCRDump(resp.Content)
	if text == "" {
		return Result{}, fmt.Errorf("ocr: empty model output")
	}
	s.Log.Info("ocr free text extracted; structuring",
		"model", resp.Model,
		"raw_len", len(text),
	)

	// Step 2: text model → schema JSON.
	structured, serr := s.structureOCRText(ctx, text)
	if serr == nil {
		structured.Model = resp.Model
		structured.LatencyMS = resp.LatencyMS + structured.LatencyMS
		return structured, nil
	}
	s.Log.Warn("ocr text structure failed; heuristic fallback",
		"err", serr.Error(),
		"raw_len", len(text),
	)
	// Step 3: deterministic heuristic from OCR text (still useful for operators).
	out := heuristicFromText(text)
	out.Model = resp.Model
	out.LatencyMS = resp.LatencyMS
	return out, nil
}

// structureOCRText asks the text model to map Free-OCR text into FailureScreenOCR JSON.
func (s *Service) structureOCRText(ctx context.Context, ocrText string) (Result, error) {
	user := "Given OCR text from a server failure screen, produce JSON with keys " +
		"raw_text, summary, category, confidence, evidence.\n" +
		"category must be one of: boot_error, media_error, hardware_error, auth_error, " +
		"network_error, installer_error, unknown.\n" +
		"confidence is 0..1. summary is one short sentence.\n" +
		"JSON only, no markdown.\n\nOCR text:\n" + ocrText
	start := time.Now()
	resp, err := s.LLM.Complete(ctx, ai.CompletionRequest{
		System: "You output only valid JSON.",
		User:   user,
	})
	if err != nil {
		return Result{}, err
	}
	out, ok := parseOCRContent(resp.Content)
	if !ok {
		return Result{}, fmt.Errorf("ocr: structure pass did not yield valid JSON")
	}
	out.LatencyMS = resp.LatencyMS
	if out.LatencyMS == 0 {
		out.LatencyMS = time.Since(start).Milliseconds()
	}
	return out, nil
}

func cleanOCRDump(content string) string {
	s := strings.TrimSpace(decode.StripCodeFences(content))
	// Drop common instruction-echo preambles if model mixed task text with OCR.
	// Prefer quoted screen phrases when present.
	if i := strings.Index(s, "No bootable"); i >= 0 {
		// keep from first likely screen phrase
		return strings.TrimSpace(s[i:])
	}
	return s
}

func heuristicFromText(text string) Result {
	lower := strings.ToLower(text)
	cat := "unknown"
	conf := 0.4
	switch {
	case strings.Contains(lower, "no bootable") || strings.Contains(lower, "boot device") ||
		strings.Contains(lower, "boot failed") || strings.Contains(lower, "pxe"):
		cat = "boot_error"
		conf = 0.7
	case strings.Contains(lower, "virtual media") || strings.Contains(lower, "media not") ||
		strings.Contains(lower, "cd/dvd"):
		cat = "media_error"
		conf = 0.7
	case strings.Contains(lower, "password") || strings.Contains(lower, "authentication") ||
		strings.Contains(lower, "login failed"):
		cat = "auth_error"
		conf = 0.65
	case strings.Contains(lower, "cpu") || strings.Contains(lower, "memory") ||
		strings.Contains(lower, "dimm") || strings.Contains(lower, "hardware"):
		cat = "hardware_error"
		conf = 0.6
	case strings.Contains(lower, "network") || strings.Contains(lower, "nic") ||
		strings.Contains(lower, "link down"):
		cat = "network_error"
		conf = 0.6
	case strings.Contains(lower, "install") || strings.Contains(lower, "anaconda") ||
		strings.Contains(lower, "cloud-init"):
		cat = "installer_error"
		conf = 0.6
	}
	// Prefer a short screen-like line for summary.
	summary := truncate(text, 200)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 4 {
			continue
		}
		ll := strings.ToLower(line)
		if strings.Contains(ll, "no bootable") || strings.Contains(ll, "error") ||
			strings.Contains(ll, "failed") || strings.Contains(ll, "virtual media") {
			summary = truncate(line, 200)
			break
		}
	}
	return Result{
		RawText:    text,
		Summary:    summary,
		Category:   cat,
		Confidence: conf,
		Evidence:   truncate(summary, 80),
	}
}

func parseOCRContent(content string) (Result, bool) {
	raw := decode.StripCodeFences(content)
	obj, err := decode.ExtractJSONObject(raw)
	if err != nil {
		return Result{}, false
	}
	var out Result
	if err := json.Unmarshal([]byte(obj), &out); err != nil {
		return Result{}, false
	}
	if err := ValidateResult(out); err != nil {
		return Result{}, false
	}
	return out, true
}

// ValidateResult checks category and confidence bounds.
func ValidateResult(r Result) error {
	switch r.Category {
	case "boot_error", "media_error", "hardware_error", "auth_error",
		"network_error", "installer_error", "unknown", "":
		if r.Category == "" {
			return fmt.Errorf("ocr: category is required")
		}
	default:
		return fmt.Errorf("ocr: invalid category %q", r.Category)
	}
	if r.Confidence < 0 || r.Confidence > 1 {
		return fmt.Errorf("ocr: confidence out of range")
	}
	if strings.TrimSpace(r.RawText) == "" && strings.TrimSpace(r.Summary) == "" {
		return fmt.Errorf("ocr: raw_text or summary required")
	}
	return nil
}

func sniffMediaType(b []byte) string {
	if len(b) >= 3 && b[0] == 0xff && b[1] == 0xd8 && b[2] == 0xff {
		return "image/jpeg"
	}
	if len(b) >= 8 && b[0] == 0x89 && b[1] == 0x50 && b[2] == 0x4e && b[3] == 0x47 {
		return "image/png"
	}
	return "image/png"
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
