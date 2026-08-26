package redfish

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	gofishredfish "github.com/stmcginnis/gofish/redfish"

	"github.com/mattcburns/shoal/internal/common/redfish/internal/ipmi"
)

// ipmiDial is DialSOL in production; tests inject timeout/port and fakes.
var (
	ipmiDial    = ipmi.DialSOL
	ipmiTimeout = 2 * time.Second
	ipmiPort    = 623
)

// OpenSOL opens a serial-over-LAN byte stream for systemID.
//
// Discovery order: classify the BMC vendor (reused from screenshot.go), try
// native Redfish/OEM WebSocket SOL candidates for recognized vendors (Dell,
// Supermicro) but only keep a socket that sniffs as line-oriented SOL text,
// then SSH when eligible (Redfish SerialConsole.SSH, manager SSH connect type,
// or Dell NetworkProtocol/OEM serial-redirection even if SerialConsole is
// empty), then IPMI 2.0 SOL (stdlib client) as last resort. Telnet-only
// BMCs are unsupported (deferred).
func (c *client) OpenSOL(ctx context.Context, systemID string) (SOLStream, error) {
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
		return SOLStream{}, err
	}

	sys, err := c.computerSystem(systemID)
	if err != nil {
		add(CaptureDebugStep{Phase: "detect", OK: false, Message: "get system: " + err.Error()})
		return SOLStream{}, err
	}
	add(CaptureDebugStep{
		Phase: "detect", OK: true,
		Message: fmt.Sprintf("system id=%s name=%s manufacturer=%s model=%s", sys.ID, sys.Name, sys.Manufacturer, sys.Model),
	})

	var mgrHints []string
	managers, mErr := api.Service.Managers()
	if mErr != nil {
		add(CaptureDebugStep{Phase: "detect", OK: false, Message: "list managers: " + mErr.Error()})
	} else {
		for _, m := range managers {
			add(CaptureDebugStep{
				Phase: "detect", OK: true,
				Message: fmt.Sprintf("manager id=%s name=%s model=%s odata=%s serial_console_types=%v",
					m.ID, m.Name, m.Model, m.ODataID, m.SerialConsole.ConnectTypesSupported),
			})
			mgrHints = append(mgrHints, m.Name, m.Model, m.ID)
		}
	}

	vendor := detectVendor(append([]string{sys.Manufacturer, sys.Model}, mgrHints...)...)
	add(CaptureDebugStep{Phase: "detect", Vendor: string(vendor), OK: true, Message: fmt.Sprintf("classified vendor=%s", vendor)})

	observed := observedConnectTypes(sys.SerialConsole, managers)

	if vendor == VendorDell || vendor == VendorSupermicro {
		if stream, wsErr := c.tryWebSocketSOL(ctx, vendor, managers, add); wsErr == nil {
			stream.Debug = dbg
			return stream, nil
		}
	}

	hint := c.sshEligible(sys, managers, vendor, add)
	if hint.eligible {
		ps := strings.TrimSpace(string(sys.PowerState))
		if ps == "" || strings.EqualFold(ps, "Off") {
			add(CaptureDebugStep{
				Phase: "probe", Vendor: string(vendor), OK: true,
				Message: fmt.Sprintf("PowerState=%s; SOL attach expected silent until power-on", ps),
			})
		}
		if stream, sshErr := c.trySSHSOL(ctx, vendor, hint, add); sshErr == nil {
			stream.Debug = dbg
			return stream, nil
		} else {
			add(CaptureDebugStep{Phase: "probe", Vendor: string(vendor), OK: false, Message: "ssh fallback failed: " + sshErr.Error()})
		}
	}

	if stream, ipmiErr := c.tryIPMISOL(ctx, sys, vendor, add); ipmiErr == nil {
		stream.Debug = dbg
		return stream, nil
	} else {
		add(CaptureDebugStep{Phase: "probe", Vendor: string(vendor), OK: false, Method: "IPMI", Message: sanitizePreview(ipmiErr.Error())})
	}

	add(CaptureDebugStep{
		Phase: "probe", Vendor: string(vendor), OK: false,
		Message: fmt.Sprintf("no supported SOL path (native WS unsupported/failed, SSH ineligible or failed, IPMI SOL failed); observed connect types: %v", observed),
	})
	return SOLStream{}, &SOLUnsupportedError{Vendor: vendor, ConnectTypes: observed, Debug: dbg}
}

func (c *client) tryIPMISOL(ctx context.Context, sys *gofishredfish.ComputerSystem, vendor VendorID, add func(CaptureDebugStep)) (SOLStream, error) {
	host, err := sshHost(c.cfg.BaseURL)
	if err != nil {
		return SOLStream{}, err
	}
	ps := strings.TrimSpace(string(sys.PowerState))
	if ps == "" || strings.EqualFold(ps, "Off") {
		add(CaptureDebugStep{
			Phase: "probe", Vendor: string(vendor), OK: true,
			Message: fmt.Sprintf("PowerState=%s; SOL attach expected silent until power-on", ps),
		})
	}
	cfg := ipmi.Config{
		Host:     host,
		Port:     ipmiPort,
		Username: c.cfg.Username,
		Password: c.cfg.Password,
		Timeout:  ipmiTimeout,
	}
	add(CaptureDebugStep{
		Phase: "request", Vendor: string(vendor), Method: "IPMI", URL: net.JoinHostPort(host, fmt.Sprintf("%d", cfg.Port)),
		Message: "dial IPMI 2.0 SOL (last resort, suite 3 then 17)",
	})
	rc, err := ipmiDial(ctx, cfg)
	if err != nil {
		return SOLStream{}, err
	}
	add(CaptureDebugStep{Phase: "request", Vendor: string(vendor), Method: "IPMI", OK: true, Message: "ipmi sol session started"})
	return SOLStream{ReadCloser: rc, Vendor: vendor, Kind: SOLConnectIPMI}, nil
}

// observedConnectTypes summarizes enabled serial-console protocols from
// Redfish's own metadata, for reporting in SOLUnsupportedError.
func observedConnectTypes(host gofishredfish.HostSerialConsole, managers []*gofishredfish.Manager) []string {
	var out []string
	if host.SSH.ServiceEnabled {
		out = append(out, "SSH")
	}
	if host.Telnet.ServiceEnabled {
		out = append(out, "Telnet")
	}
	if host.IPMI.ServiceEnabled {
		out = append(out, "IPMI")
	}
	for _, m := range managers {
		for _, ct := range m.SerialConsole.ConnectTypesSupported {
			out = append(out, "Manager:"+string(ct))
		}
	}
	return out
}

// --- Native WebSocket SOL (Dell, Supermicro) ---
//
// No vendor publishes documented, verified client-pull plain-text SOL over
// WebSocket at time of writing; Dell iDRAC and Supermicro console redirection
// is typically an HTML5/binary KVM protocol, not line-oriented SOL. The
// candidates below are unverified placeholders following the same
// probe-and-record pattern as captureDell/captureSupermicro in screenshot.go.
// docs/real-hardware-sol-runbook.md tracks closing this gap on real hardware.

func (c *client) tryWebSocketSOL(ctx context.Context, vendor VendorID, managers []*gofishredfish.Manager, add func(CaptureDebugStep)) (SOLStream, error) {
	if !strings.EqualFold(c.cfg.AuthMode, "basic") && c.cfg.AuthMode != "" {
		add(CaptureDebugStep{
			Phase: "probe", Vendor: string(vendor), OK: false,
			Message: fmt.Sprintf("websocket SOL auth for redfish_auth_mode=%q not implemented (only basic); skipping WS candidates", c.cfg.AuthMode),
		})
		return SOLStream{}, fmt.Errorf("redfish: websocket sol: unsupported auth mode %q", c.cfg.AuthMode)
	}

	candidates := solWSCandidates(vendor, c.cfg.BaseURL, managers)
	if len(candidates) == 0 {
		return SOLStream{}, fmt.Errorf("redfish: no websocket SOL candidates for vendor %q", vendor)
	}

	httpClient, err := c.httpClient()
	if err != nil {
		return SOLStream{}, err
	}
	header := http.Header{}
	if c.cfg.Username != "" || c.cfg.Password != "" {
		header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(c.cfg.Username+":"+c.cfg.Password)))
	}

	var lastErr error
	for _, candURL := range candidates {
		start := time.Now()
		add(CaptureDebugStep{Phase: "request", Vendor: string(vendor), Method: http.MethodGet, URL: candURL, Message: "websocket SOL dial (unverified candidate)"})
		dialCtx, cancelDial := context.WithTimeout(ctx, 3*time.Second)
		conn, status, dialErr := wsDial(dialCtx, candURL, httpClient, header)
		cancelDial()
		elapsed := time.Since(start).Milliseconds()
		if dialErr != nil {
			add(CaptureDebugStep{
				Phase: "request", Vendor: string(vendor), Method: http.MethodGet, URL: candURL,
				StatusCode: status, OK: false, Message: sanitizePreview(dialErr.Error()), ElapsedMS: elapsed,
			})
			lastErr = dialErr
			continue
		}
		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		opcode, frame, sniffErr := conn.ReadMessage()
		_ = conn.SetReadDeadline(time.Time{})
		if sniffErr != nil || opcode != wsOpText || !looksLikeSOLText(frame) {
			why := "sniff timeout/silence"
			if sniffErr == nil && opcode != wsOpText {
				if opcode == wsOpClose {
					why = "websocket closed by server"
				} else {
					why = "binary websocket frame"
				}
			} else if sniffErr == nil {
				why = "html or non-SOL text"
			}
			add(CaptureDebugStep{
				Phase: "parse", Vendor: string(vendor), URL: candURL, OK: false,
				Message:   "websocket not line-oriented SOL (" + why + "); falling through",
				ElapsedMS: time.Since(start).Milliseconds(),
			})
			_ = conn.Close()
			lastErr = fmt.Errorf("websocket sol sniff: %s", why)
			continue
		}
		add(CaptureDebugStep{Phase: "request", Vendor: string(vendor), Method: http.MethodGet, URL: candURL, OK: true, Message: "websocket connected (SOL text)", ElapsedMS: elapsed})
		rest := conn.Reader()
		return SOLStream{
			ReadCloser: &wsSOLCloser{r: io.MultiReader(bytes.NewReader(frame), rest), c: conn},
			Vendor:     vendor,
			Kind:       SOLConnectWebSocket,
		}, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no candidates")
	}
	return SOLStream{}, fmt.Errorf("redfish: websocket sol: %w", lastErr)
}

// solWSCandidates returns unverified, best-effort candidate WebSocket SOL URLs
// for a vendor. base is the BMC's Config.BaseURL (http(s)://host[:port]).
func solWSCandidates(vendor VendorID, base string, managers []*gofishredfish.Manager) []string {
	u, err := url.Parse(base)
	if err != nil {
		return nil
	}
	wsScheme := "ws"
	if strings.EqualFold(u.Scheme, "https") {
		wsScheme = "wss"
	}
	wsBase := wsScheme + "://" + u.Host

	var names []string
	for _, m := range managers {
		names = append(names, strings.TrimSuffix(m.ODataID, "/"))
	}
	if len(names) == 0 {
		names = []string{""}
	}

	var out []string
	switch vendor {
	case VendorDell:
		for range names {
			// iDRAC virtual console websocket paths observed across firmware in
			// community tooling; not part of any public Dell API reference.
			out = append(out,
				wsBase+"/console",
				wsBase+"/wsman/virtualconsole",
			)
		}
	case VendorSupermicro:
		for range names {
			out = append(out,
				wsBase+"/kvms/0",
				wsBase+"/console/sol",
			)
		}
	}
	return out
}

type wsSOLCloser struct {
	r io.Reader
	c io.Closer
}

func (w *wsSOLCloser) Read(p []byte) (int, error) { return w.r.Read(p) }
func (w *wsSOLCloser) Close() error               { return w.c.Close() }

func looksLikeSOLText(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	s := strings.ToLower(string(b))
	if strings.Contains(s, "<!doctype") || strings.Contains(s, "<html") {
		return false
	}
	if bytes.Contains(b, []byte("SHOAL|")) {
		return true
	}
	printable := 0
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		i += size
		if r == utf8.RuneError && size == 1 {
			continue
		}
		if r == '\n' || r == '\r' || r == '\t' || (r >= 32 && r < 127) {
			printable++
		}
	}
	runes := utf8.RuneCount(b)
	if runes == 0 {
		return false
	}
	return printable*10 >= runes*8
}
