package redfish

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

// ScreenshotKind selects which capture to request when the vendor supports it.
type ScreenshotKind string

const (
	// ScreenshotCurrent is the live/current server console frame when available.
	ScreenshotCurrent ScreenshotKind = "current"
	// ScreenshotLastCrash is the last crash/pre-boot failure screen when available.
	ScreenshotLastCrash ScreenshotKind = "last_crash"
)

// VendorID is a coarse BMC vendor classification for OEM capture paths.
type VendorID string

const (
	VendorUnknown    VendorID = "unknown"
	VendorDell       VendorID = "dell"
	VendorSupermicro VendorID = "supermicro"
)

// CaptureDebugStep is one probe/HTTP attempt for operator debugging.
// Secrets (passwords, tokens) must never appear in Message or BodyPreview.
type CaptureDebugStep struct {
	At          time.Time `json:"at"`
	Phase       string    `json:"phase"` // detect | probe | request | parse
	Vendor      string    `json:"vendor,omitempty"`
	Method      string    `json:"method,omitempty"`
	URL         string    `json:"url,omitempty"`
	StatusCode  int       `json:"status_code,omitempty"`
	OK          bool      `json:"ok"`
	Message     string    `json:"message"`
	BodyPreview string    `json:"body_preview,omitempty"` // truncated, redacted
	ElapsedMS   int64     `json:"elapsed_ms,omitempty"`
}

// Screenshot is a captured console frame plus rich debug for vendor paths.
type Screenshot struct {
	Image     []byte         `json:"image_base64,omitempty"` // filled for API only when needed; CLI omits large blobs
	MediaType string         `json:"media_type,omitempty"`
	Vendor    VendorID       `json:"vendor,omitempty"`
	Kind      ScreenshotKind `json:"kind,omitempty"`
	// Source describes how the image was obtained (e.g. dell_export_server_screenshot).
	Source string `json:"source,omitempty"`
	// Debug is ordered steps useful when OEM paths fail on real hardware.
	Debug []CaptureDebugStep `json:"debug,omitempty"`
}

// CaptureScreenshot tries OEM/documented capture paths (Dell, Supermicro first).
// Returns a structured error with Debug populated when unsupported or failed.
// Callers should log Debug at info/debug level (no secrets).
func (c *client) CaptureScreenshot(ctx context.Context, systemID string, kind ScreenshotKind) (Screenshot, error) {
	if kind == "" {
		kind = ScreenshotCurrent
	}
	var dbg []CaptureDebugStep
	add := func(step CaptureDebugStep) {
		if step.At.IsZero() {
			step.At = time.Now().UTC()
		}
		if len(step.BodyPreview) > 400 {
			step.BodyPreview = step.BodyPreview[:400] + "…"
		}
		dbg = append(dbg, step)
	}

	api, err := c.apiClient()
	if err != nil {
		return Screenshot{Debug: dbg}, err
	}

	// Multi-system BMCs (lab sushy) need an explicit id; if empty, pick the first and record it.
	if systemID == "" {
		systems, lerr := c.ListSystems(ctx)
		if lerr != nil {
			add(CaptureDebugStep{Phase: "detect", OK: false, Message: "list systems: " + lerr.Error()})
			return Screenshot{Debug: dbg}, lerr
		}
		if len(systems) == 0 {
			add(CaptureDebugStep{Phase: "detect", OK: false, Message: "no systems"})
			return Screenshot{Debug: dbg}, fmt.Errorf("redfish: no systems")
		}
		systemID = systems[0].ID
		add(CaptureDebugStep{
			Phase: "detect", OK: true,
			Message: fmt.Sprintf("system_id empty; using first of %d systems id=%s name=%s", len(systems), systems[0].ID, systems[0].Name),
		})
	}
	sys, err := c.GetSystem(ctx, systemID)
	if err != nil {
		add(CaptureDebugStep{Phase: "detect", OK: false, Message: "get system: " + err.Error()})
		return Screenshot{Debug: dbg}, err
	}
	add(CaptureDebugStep{
		Phase:   "detect",
		OK:      true,
		Message: fmt.Sprintf("system id=%s name=%s manufacturer=%s model=%s", sys.ID, sys.Name, sys.Manufacturer, sys.Model),
	})

	// List managers for OEM actions and manufacturer hints.
	var mgrHints []string
	managers, err := api.Service.Managers()
	if err != nil {
		add(CaptureDebugStep{Phase: "detect", OK: false, Message: "list managers: " + err.Error()})
	} else {
		for _, m := range managers {
			add(CaptureDebugStep{
				Phase:   "detect",
				OK:      true,
				Message: fmt.Sprintf("manager id=%s name=%s model=%s odata=%s", m.ID, m.Name, m.Model, m.ODataID),
			})
			mgrHints = append(mgrHints, m.Name, m.Model, m.ID)
		}
	}

	vendor := detectVendor(append([]string{sys.Manufacturer, sys.Model}, mgrHints...)...)
	add(CaptureDebugStep{
		Phase:   "detect",
		Vendor:  string(vendor),
		OK:      true,
		Message: fmt.Sprintf("classified vendor=%s", vendor),
	})

	switch vendor {
	case VendorDell:
		shot, err := c.captureDell(ctx, kind, add)
		shot.Debug = dbg
		if err != nil {
			return shot, fmt.Errorf("%w\n%s", err, formatDebug(dbg))
		}
		return shot, nil
	case VendorSupermicro:
		shot, err := c.captureSupermicro(ctx, kind, add)
		shot.Debug = dbg
		if err != nil {
			return shot, fmt.Errorf("%w\n%s", err, formatDebug(dbg))
		}
		return shot, nil
	default:
		add(CaptureDebugStep{
			Phase:   "probe",
			Vendor:  string(vendor),
			OK:      false,
			Message: "no OEM screenshot adapter for this manufacturer (supported: Dell, Supermicro); use -file for operator capture",
		})
		return Screenshot{Debug: dbg, Vendor: vendor, Kind: kind},
			fmt.Errorf("redfish: screenshot unsupported for vendor %q\n%s", vendor, formatDebug(dbg))
	}
}

func detectVendor(parts ...string) VendorID {
	joined := strings.ToLower(strings.Join(parts, " "))
	if strings.Contains(joined, "dell") || strings.Contains(joined, "idrac") {
		return VendorDell
	}
	if strings.Contains(joined, "supermicro") || strings.Contains(joined, "super micro") || strings.Contains(joined, "smci") {
		return VendorSupermicro
	}
	return VendorUnknown
}

// --- Dell OEM (public iDRAC Redfish scripting) ---
// Typical action (iDRAC9+ OEM):
//
//	POST .../Managers/{id}/Actions/Oem/EID_674_Manager.ExportServerScreenShot
//	{"FileType":"ServerScreenShot"|"LastCrashScreenShot"}
//
// Response often includes base64 image data or a download URI.
// Paths are attempted with rich debug; real hardware will refine fixtures.

func (c *client) captureDell(ctx context.Context, kind ScreenshotKind, add func(CaptureDebugStep)) (Screenshot, error) {
	_ = ctx
	api, err := c.apiClient()
	if err != nil {
		return Screenshot{}, err
	}
	fileType := "ServerScreenShot"
	if kind == ScreenshotLastCrash {
		fileType = "LastCrashScreenShot"
	}
	// Resolve manager OData IDs from live service.
	mgrs, err := api.Service.Managers()
	if err != nil {
		add(CaptureDebugStep{Phase: "probe", Vendor: "dell", OK: false, Message: err.Error()})
		return Screenshot{Vendor: VendorDell, Kind: kind}, err
	}
	var lastErr error
	for _, m := range mgrs {
		base := strings.TrimSuffix(m.ODataID, "/")
		// Candidate action URIs used across iDRAC versions / docs.
		actions := []string{
			base + "/Actions/Oem/EID_674_Manager.ExportServerScreenShot",
			base + "/Actions/Oem/DellManager.ExportServerScreenShot",
			base + "/Actions/Oem/OemManager.ExportServerScreenShot",
		}
		for _, actionURL := range actions {
			start := time.Now()
			payload := map[string]string{"FileType": fileType}
			add(CaptureDebugStep{
				Phase:   "request",
				Vendor:  "dell",
				Method:  http.MethodPost,
				URL:     actionURL,
				Message: fmt.Sprintf("ExportServerScreenShot FileType=%s", fileType),
			})
			resp, err := api.Post(actionURL, payload)
			elapsed := time.Since(start).Milliseconds()
			if err != nil {
				add(CaptureDebugStep{
					Phase: "request", Vendor: "dell", Method: http.MethodPost, URL: actionURL,
					OK: false, Message: err.Error(), ElapsedMS: elapsed,
				})
				lastErr = err
				continue
			}
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			_ = resp.Body.Close()
			preview := sanitizePreview(string(body))
			add(CaptureDebugStep{
				Phase: "request", Vendor: "dell", Method: http.MethodPost, URL: actionURL,
				StatusCode: resp.StatusCode, OK: resp.StatusCode >= 200 && resp.StatusCode < 300,
				Message: http.StatusText(resp.StatusCode), BodyPreview: preview, ElapsedMS: elapsed,
			})
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				lastErr = fmt.Errorf("dell screenshot: HTTP %d", resp.StatusCode)
				continue
			}
			img, mt, perr := parseImagePayload(body, resp.Header.Get("Content-Type"))
			if perr != nil {
				add(CaptureDebugStep{
					Phase: "parse", Vendor: "dell", OK: false,
					Message: perr.Error(), BodyPreview: preview,
				})
				lastErr = perr
				continue
			}
			add(CaptureDebugStep{
				Phase: "parse", Vendor: "dell", OK: true,
				Message: fmt.Sprintf("decoded image bytes=%d media_type=%s", len(img), mt),
			})
			return Screenshot{
				Image: img, MediaType: mt, Vendor: VendorDell, Kind: kind,
				Source: "dell_export_server_screenshot",
			}, nil
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("dell screenshot: no managers or all action paths failed")
	}
	add(CaptureDebugStep{Phase: "probe", Vendor: "dell", OK: false, Message: lastErr.Error()})
	return Screenshot{Vendor: VendorDell, Kind: kind}, lastErr
}

// --- Supermicro (public Redfish user guide: Create/Download ScreenCapture) ---
// Documented flow (version-dependent):
//  1. Create ScreenCapture (POST OEM action)
//  2. Download ScreenCapture (GET resulting resource)
// Paths below are best-effort candidates; debug records every attempt.

func (c *client) captureSupermicro(ctx context.Context, kind ScreenshotKind, add func(CaptureDebugStep)) (Screenshot, error) {
	_ = ctx
	api, err := c.apiClient()
	if err != nil {
		return Screenshot{}, err
	}
	mgrs, err := api.Service.Managers()
	if err != nil {
		add(CaptureDebugStep{Phase: "probe", Vendor: "supermicro", OK: false, Message: err.Error()})
		return Screenshot{Vendor: VendorSupermicro, Kind: kind}, err
	}
	// Prefer crash capture kind when requested.
	createNames := []string{"ScreenCapture", "CrashScreenCapture"}
	if kind == ScreenshotLastCrash {
		createNames = []string{"CrashScreenCapture", "ScreenCapture"}
	}
	var lastErr error
	for _, m := range mgrs {
		base := strings.TrimSuffix(m.ODataID, "/")
		for _, name := range createNames {
			// Common SuperMicro OEM action patterns (refine with real hardware fixtures).
			createURLs := []string{
				base + "/Actions/Oem/SmcManagerActions." + name,
				base + "/Actions/Oem/SmcManager." + name,
				base + "/Oem/Supermicro/ScreenCapture",
				base + "/Oem/Supermicro/" + name,
			}
			for _, createURL := range createURLs {
				start := time.Now()
				add(CaptureDebugStep{
					Phase: "request", Vendor: "supermicro", Method: http.MethodPost, URL: createURL,
					Message: "create " + name,
				})
				resp, err := api.Post(createURL, map[string]any{})
				elapsed := time.Since(start).Milliseconds()
				if err != nil {
					add(CaptureDebugStep{
						Phase: "request", Vendor: "supermicro", Method: http.MethodPost, URL: createURL,
						OK: false, Message: err.Error(), ElapsedMS: elapsed,
					})
					lastErr = err
					continue
				}
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
				_ = resp.Body.Close()
				preview := sanitizePreview(string(body))
				add(CaptureDebugStep{
					Phase: "request", Vendor: "supermicro", Method: http.MethodPost, URL: createURL,
					StatusCode: resp.StatusCode, OK: resp.StatusCode >= 200 && resp.StatusCode < 300,
					Message: http.StatusText(resp.StatusCode), BodyPreview: preview, ElapsedMS: elapsed,
				})
				if resp.StatusCode < 200 || resp.StatusCode >= 300 {
					lastErr = fmt.Errorf("supermicro create %s: HTTP %d", name, resp.StatusCode)
					continue
				}
				// Immediate image in body?
				if img, mt, perr := parseImagePayload(body, resp.Header.Get("Content-Type")); perr == nil && len(img) > 0 {
					add(CaptureDebugStep{Phase: "parse", Vendor: "supermicro", OK: true,
						Message: fmt.Sprintf("image in create response bytes=%d", len(img))})
					return Screenshot{
						Image: img, MediaType: mt, Vendor: VendorSupermicro, Kind: kind,
						Source: "supermicro_" + strings.ToLower(name),
					}, nil
				}
				// Follow download URI from JSON if present.
				downloadURL := extractDownloadURL(body, base)
				if downloadURL == "" {
					// Heuristic download paths.
					downloadURL = base + "/Oem/Supermicro/" + name + "/Download"
				}
				start2 := time.Now()
				add(CaptureDebugStep{
					Phase: "request", Vendor: "supermicro", Method: http.MethodGet, URL: downloadURL,
					Message: "download " + name,
				})
				dresp, derr := api.Get(downloadURL)
				elapsed2 := time.Since(start2).Milliseconds()
				if derr != nil {
					add(CaptureDebugStep{
						Phase: "request", Vendor: "supermicro", Method: http.MethodGet, URL: downloadURL,
						OK: false, Message: derr.Error(), ElapsedMS: elapsed2,
					})
					lastErr = derr
					continue
				}
				dbody, _ := io.ReadAll(io.LimitReader(dresp.Body, 4<<20))
				_ = dresp.Body.Close()
				dpreview := sanitizePreview(string(dbody))
				add(CaptureDebugStep{
					Phase: "request", Vendor: "supermicro", Method: http.MethodGet, URL: downloadURL,
					StatusCode: dresp.StatusCode, OK: dresp.StatusCode >= 200 && dresp.StatusCode < 300,
					Message: http.StatusText(dresp.StatusCode), BodyPreview: dpreview, ElapsedMS: elapsed2,
				})
				if dresp.StatusCode < 200 || dresp.StatusCode >= 300 {
					lastErr = fmt.Errorf("supermicro download: HTTP %d", dresp.StatusCode)
					continue
				}
				img, mt, perr := parseImagePayload(dbody, dresp.Header.Get("Content-Type"))
				if perr != nil {
					add(CaptureDebugStep{Phase: "parse", Vendor: "supermicro", OK: false, Message: perr.Error()})
					lastErr = perr
					continue
				}
				add(CaptureDebugStep{Phase: "parse", Vendor: "supermicro", OK: true,
					Message: fmt.Sprintf("decoded image bytes=%d media_type=%s", len(img), mt)})
				return Screenshot{
					Image: img, MediaType: mt, Vendor: VendorSupermicro, Kind: kind,
					Source: "supermicro_" + strings.ToLower(name),
				}, nil
			}
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("supermicro screenshot: no managers or all paths failed")
	}
	add(CaptureDebugStep{Phase: "probe", Vendor: "supermicro", OK: false, Message: lastErr.Error()})
	return Screenshot{Vendor: VendorSupermicro, Kind: kind}, lastErr
}

func parseImagePayload(body []byte, contentType string) ([]byte, string, error) {
	ct := strings.ToLower(contentType)
	if strings.Contains(ct, "image/jpeg") || strings.Contains(ct, "image/jpg") {
		return body, "image/jpeg", nil
	}
	if strings.Contains(ct, "image/png") {
		return body, "image/png", nil
	}
	// Raw magic
	if len(body) >= 3 && body[0] == 0xff && body[1] == 0xd8 {
		return body, "image/jpeg", nil
	}
	if len(body) >= 4 && body[0] == 0x89 && body[1] == 'P' && body[2] == 'N' && body[3] == 'G' {
		return body, "image/png", nil
	}
	// JSON with base64 field
	var m map[string]any
	if err := json.Unmarshal(body, &m); err == nil {
		for _, key := range []string{"ServerScreenShot", "server_screenshot", "Image", "Data", "Base64Image", "Screenshot"} {
			if v, ok := m[key].(string); ok && v != "" {
				raw, err := base64.StdEncoding.DecodeString(v)
				if err != nil {
					// try raw URL encoding ignore
					raw, err = base64.RawStdEncoding.DecodeString(v)
				}
				if err != nil {
					return nil, "", fmt.Errorf("base64 decode %s: %w", key, err)
				}
				mt := "image/jpeg"
				if len(raw) >= 4 && raw[0] == 0x89 {
					mt = "image/png"
				}
				return raw, mt, nil
			}
		}
	}
	return nil, "", fmt.Errorf("no image payload in response (content-type=%q bytes=%d)", contentType, len(body))
}

func extractDownloadURL(body []byte, managerBase string) string {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return ""
	}
	for _, key := range []string{"@odata.id", "Download", "URI", "Url", "url", "Location"} {
		if v, ok := m[key].(string); ok && v != "" {
			if strings.HasPrefix(v, "http") || strings.HasPrefix(v, "/") {
				return v
			}
			return managerBase + "/" + strings.TrimPrefix(v, "/")
		}
	}
	return ""
}

func sanitizePreview(s string) string {
	// Never leave password-like fields in debug previews.
	lower := strings.ToLower(s)
	for _, bad := range []string{"password", "passwd", "secret", "token", "authorization"} {
		if strings.Contains(lower, bad) {
			return "[redacted: body contained sensitive key]"
		}
	}
	// Collapse whitespace for logs.
	s = string(bytes.Join(bytes.Fields([]byte(s)), []byte(" ")))
	if len(s) > 400 {
		return s[:400] + "…"
	}
	return s
}

func formatDebug(steps []CaptureDebugStep) string {
	var b strings.Builder
	b.WriteString("screenshot debug:\n")
	for i, s := range steps {
		fmt.Fprintf(&b, "  %d. [%s] ok=%v", i+1, s.Phase, s.OK)
		if s.Vendor != "" {
			fmt.Fprintf(&b, " vendor=%s", s.Vendor)
		}
		if s.Method != "" {
			fmt.Fprintf(&b, " %s", s.Method)
		}
		if s.URL != "" {
			fmt.Fprintf(&b, " %s", s.URL)
		}
		if s.StatusCode != 0 {
			fmt.Fprintf(&b, " status=%d", s.StatusCode)
		}
		if s.Message != "" {
			fmt.Fprintf(&b, " — %s", s.Message)
		}
		if s.BodyPreview != "" {
			fmt.Fprintf(&b, " body=%q", s.BodyPreview)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
