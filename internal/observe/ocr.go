package observe

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/core/ocr"
)

// OCRInput is operator or BMC-sourced failure-screen OCR request.
type OCRInput struct {
	DeviceID  string
	JobID     string // optional correlation only (not a lifecycle transition)
	Image     []byte
	MediaType string
	// Screenshot from Redfish when Image empty and BMC set.
	BMC      redfish.BMC
	SystemID string
	Kind     redfish.ScreenshotKind
	// Persist writes a telemetry event when Telemetry is configured.
	Persist bool
}

// OCROutput is the OCR result plus optional capture debug and event id.
type OCROutput struct {
	Result    ocr.Result          `json:"result"`
	EventID   string              `json:"event_id,omitempty"`
	Source    string              `json:"source"`            // file | redfish_dell | redfish_supermicro | …
	Capture   *redfish.Screenshot `json:"capture,omitempty"` // includes Debug for vendor paths
	ImageInfo map[string]any      `json:"image_info,omitempty"`
}

// OCRFailureScreen runs Core vision OCR and optionally persists a graphics_ocr event.
// Does not modify job lifecycle state.
func (s *Service) OCRFailureScreen(ctx context.Context, ocrSvc *ocr.Service, in OCRInput) (OCROutput, error) {
	if in.DeviceID == "" {
		return OCROutput{}, fmt.Errorf("observe: device_id required")
	}
	if ocrSvc == nil {
		return OCROutput{}, fmt.Errorf("observe: ocr service not configured (AI required)")
	}

	out := OCROutput{ImageInfo: map[string]any{}}
	img := in.Image
	mt := in.MediaType
	source := "file"

	if len(img) == 0 {
		if in.BMC == nil {
			return OCROutput{}, fmt.Errorf("observe: image required (or BMC for Redfish capture)")
		}
		kind := in.Kind
		if kind == "" {
			kind = redfish.ScreenshotCurrent
		}
		shot, err := in.BMC.CaptureScreenshot(ctx, in.SystemID, kind)
		// Always surface capture debug for operators (omit raw image bytes from JSON).
		cap := shot
		cap.Image = nil
		out.Capture = &cap
		if err != nil {
			if s.Log != nil {
				s.Log.Warn("redfish screenshot failed",
					"device_id", in.DeviceID,
					"vendor", string(shot.Vendor),
					"debug_steps", len(shot.Debug),
					"err", err.Error(),
				)
				for i, step := range shot.Debug {
					s.Log.Info("screenshot debug step",
						"i", i,
						"phase", step.Phase,
						"vendor", step.Vendor,
						"method", step.Method,
						"url", step.URL,
						"status", step.StatusCode,
						"ok", step.OK,
						"msg", step.Message,
						// body_preview already sanitized in redfish package
						"body_preview", step.BodyPreview,
					)
				}
			}
			return out, fmt.Errorf("observe: redfish capture: %w", err)
		}
		img = shot.Image
		mt = shot.MediaType
		source = "redfish_" + string(shot.Vendor)
		if shot.Source != "" {
			source = shot.Source
		}
	}

	out.Source = source
	out.ImageInfo["bytes"] = len(img)
	out.ImageInfo["media_type"] = mt

	res, err := ocrSvc.AnalyzeFailureScreen(ctx, img, mt)
	if err != nil {
		return out, err
	}
	out.Result = res

	if in.Persist {
		if s.Telemetry == nil {
			return out, fmt.Errorf("observe: persist requires telemetry store (SHOAL_TELEMETRY_DATABASE_URL)")
		}
		msg := res.Summary
		if msg == "" {
			msg = res.RawText
		}
		if len(msg) > 500 {
			msg = msg[:500] + "…"
		}
		evID := newOCREventID()
		ev := models.NormalizedEvent{
			ID:        evID,
			DeviceID:  in.DeviceID,
			EventType: "graphics_ocr",
			Severity:  "error",
			Component: "bmc_graphics",
			Message:   fmt.Sprintf("[%s] %s", res.Category, msg),
			Timestamp: time.Now().UTC(),
			// Raw is not persisted by telemetry Store (by design); keep category in message.
		}
		if err := s.Telemetry.WriteEvent(ctx, ev); err != nil {
			return out, fmt.Errorf("observe: write event: %w", err)
		}
		out.EventID = evID
		if s.Log != nil {
			s.Log.Info("graphics ocr event written",
				"device_id", in.DeviceID,
				"event_id", evID,
				"category", res.Category,
				"source", source,
			)
		}
	}
	return out, nil
}

func newOCREventID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "ocr-" + hex.EncodeToString(b[:])
}
