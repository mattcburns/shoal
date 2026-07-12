package reconcile

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mattcburns/shoal/internal/common/models"
)

// OCR prompt for vision models. deepseek-ocr responds best to "Free OCR."
// Generic VLMs still produce a caption that may contain readable tokens.
const photoOCRPrompt = "Free OCR."

var (
	reLabeledSerial = regexp.MustCompile(`(?i)\b(?:serial(?:\s*(?:number|no\.?|#))?|sn)\s*[:#\-]?\s*([A-Za-z0-9][A-Za-z0-9._\-]{2,39})`)
	reLabeledVendor = regexp.MustCompile(`(?i)\b(?:vendor|manufacturer|make)\s*[:#\-]?\s*([^\n,;]+)`)
	reLabeledModel  = regexp.MustCompile(`(?i)\b(?:model|sku|part(?:\s*number)?)\s*[:#\-]?\s*([^\n,;]+)`)
)

// ocrFields are identity values parsed from OCR/caption text.
type ocrFields struct {
	Serial  string
	Vendor  string
	Model   string
	RawText string
}

// parseOCRIdentity extracts SERIAL/VENDOR/MODEL style labels from OCR text.
func parseOCRIdentity(desc string) ocrFields {
	out := ocrFields{RawText: strings.TrimSpace(desc)}
	if out.RawText == "" {
		return out
	}
	norm := strings.ReplaceAll(out.RawText, "##", "")
	norm = strings.ReplaceAll(norm, "**", "")

	if m := reLabeledSerial.FindStringSubmatch(norm); len(m) == 2 {
		out.Serial = cleanOCRToken(m[1])
	}
	if m := reLabeledVendor.FindStringSubmatch(norm); len(m) == 2 {
		out.Vendor = cleanOCRToken(m[1])
	}
	if m := reLabeledModel.FindStringSubmatch(norm); len(m) == 2 {
		out.Model = cleanOCRToken(m[1])
	}

	for _, line := range strings.Split(norm, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				switch strings.ToUpper(fields[0]) {
				case "VENDOR", "MANUFACTURER", "MAKE":
					if out.Vendor == "" {
						out.Vendor = cleanOCRToken(strings.Join(fields[1:], " "))
					}
				case "MODEL", "SKU":
					if out.Model == "" {
						out.Model = cleanOCRToken(strings.Join(fields[1:], " "))
					}
				case "SERIAL", "SN":
					if out.Serial == "" {
						out.Serial = cleanOCRToken(fields[1])
					}
				}
			}
			continue
		}
		k := strings.ToUpper(strings.TrimSpace(key))
		v := cleanOCRToken(val)
		if v == "" {
			continue
		}
		switch {
		case strings.Contains(k, "SERIAL") || k == "SN":
			if out.Serial == "" {
				out.Serial = v
			}
		case strings.Contains(k, "VENDOR") || strings.Contains(k, "MANUFACTURER") || k == "MAKE":
			if out.Vendor == "" {
				out.Vendor = v
			}
		case strings.Contains(k, "MODEL") || k == "SKU":
			if out.Model == "" {
				out.Model = v
			}
		}
	}
	return out
}

func cleanOCRToken(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, ".,;:\"'`")
	return strings.Join(strings.Fields(s), " ")
}

// resultFromOCR builds a NormalizationResult from parsed OCR + operator BMC IP.
// Requires a real serial from OCR — never invents placeholders.
func resultFromOCR(ocr ocrFields, bmcIP string) (models.NormalizationResult, error) {
	if ocr.Serial == "" {
		return models.NormalizationResult{}, errNoSerialFromPhoto(ocr.RawText)
	}
	if bmcIP == "" {
		return models.NormalizationResult{}, fmt.Errorf("reconcile: photo: bmc_ip is required for photo ingest (operator -bmc-ip)")
	}
	r := models.NormalizationResult{
		Asset: models.NormalizedAsset{
			Serial: ocr.Serial,
			Vendor: ocr.Vendor,
			Model:  ocr.Model,
			BMCIP:  bmcIP,
		},
		Confidences: []models.FieldConfidence{
			{Field: "serial", Confidence: 0.9, Source: "ai", Evidence: excerpt(ocr.RawText, ocr.Serial)},
			{Field: "bmc_ip", Confidence: 0.99, Source: "ai", Evidence: "operator bmc_ip: " + bmcIP},
		},
		NeedsReview: ocr.Vendor == "" || ocr.Model == "",
	}
	if ocr.Vendor != "" {
		r.Confidences = append(r.Confidences, models.FieldConfidence{
			Field: "vendor", Confidence: 0.85, Source: "ai", Evidence: excerpt(ocr.RawText, ocr.Vendor),
		})
	}
	if ocr.Model != "" {
		r.Confidences = append(r.Confidences, models.FieldConfidence{
			Field: "model", Confidence: 0.85, Source: "ai", Evidence: excerpt(ocr.RawText, ocr.Model),
		})
	}
	return r, nil
}

func excerpt(text, needle string) string {
	if needle == "" {
		return "photo ocr"
	}
	if i := strings.Index(strings.ToLower(text), strings.ToLower(needle)); i >= 0 {
		start := i - 20
		if start < 0 {
			start = 0
		}
		end := i + len(needle) + 20
		if end > len(text) {
			end = len(text)
		}
		return strings.TrimSpace(text[start:end])
	}
	return needle
}

func errNoSerialFromPhoto(ocrText string) error {
	snip := strings.TrimSpace(ocrText)
	if len(snip) > 200 {
		snip = snip[:200] + "…"
	}
	if snip == "" {
		return fmt.Errorf("reconcile: photo: vision OCR returned no text; use a clearer label photo or SHOAL_AI_VISION_MODEL=deepseek-ocr")
	}
	return fmt.Errorf("reconcile: photo: could not extract serial from OCR text %q", snip)
}
