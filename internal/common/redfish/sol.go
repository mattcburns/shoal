package redfish

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	gofishredfish "github.com/stmcginnis/gofish/redfish"
	"golang.org/x/crypto/ssh"

	"github.com/coder/websocket"
)

// OpenSOL opens a serial-over-LAN byte stream for systemID.
//
// Discovery order: classify the BMC vendor (reused from screenshot.go), try
// native Redfish/OEM WebSocket SOL candidates for recognized vendors (Dell,
// Supermicro — unverified placeholder endpoints; see docs/real-hardware-sol-runbook.md),
// then — only if Redfish's own capability metadata
// (ComputerSystem.SerialConsole.SSH / Manager.SerialConsole) advertises SSH —
// fall back to an SSH session using the ComputerSystem-provided
// ConsoleEntryCommand when present. Raw IPMI is never attempted, and a
// Telnet-only BMC is treated as unsupported (deferred, not implemented).
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

	if sys.SerialConsole.SSH.ServiceEnabled {
		if stream, sshErr := c.trySSHSOL(ctx, sys, vendor, add); sshErr == nil {
			stream.Debug = dbg
			return stream, nil
		} else {
			add(CaptureDebugStep{Phase: "probe", Vendor: string(vendor), OK: false, Message: "ssh fallback failed: " + sshErr.Error()})
		}
	}

	add(CaptureDebugStep{
		Phase: "probe", Vendor: string(vendor), OK: false,
		Message: fmt.Sprintf("no supported SOL path (native WS unsupported/failed, Redfish did not advertise SSH); observed connect types: %v", observed),
	})
	return SOLStream{}, &SOLUnsupportedError{Vendor: vendor, ConnectTypes: observed, Debug: dbg}
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
		conn, resp, dialErr := websocket.Dial(ctx, candURL, &websocket.DialOptions{
			HTTPClient: httpClient,
			HTTPHeader: header,
		})
		elapsed := time.Since(start).Milliseconds()
		if dialErr != nil {
			status := 0
			if resp != nil {
				status = resp.StatusCode
			}
			add(CaptureDebugStep{
				Phase: "request", Vendor: string(vendor), Method: http.MethodGet, URL: candURL,
				StatusCode: status, OK: false, Message: sanitizePreview(dialErr.Error()), ElapsedMS: elapsed,
			})
			lastErr = dialErr
			continue
		}
		add(CaptureDebugStep{Phase: "request", Vendor: string(vendor), Method: http.MethodGet, URL: candURL, OK: true, Message: "websocket connected", ElapsedMS: elapsed})
		return SOLStream{
			ReadCloser: websocket.NetConn(context.Background(), conn, websocket.MessageText),
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

// --- SSH fallback (Redfish-advertised only; never raw IPMI, never guessed) ---

func (c *client) trySSHSOL(ctx context.Context, sys *gofishredfish.ComputerSystem, vendor VendorID, add func(CaptureDebugStep)) (SOLStream, error) {
	proto := sys.SerialConsole.SSH
	host, err := sshHost(c.cfg.BaseURL)
	if err != nil {
		add(CaptureDebugStep{Phase: "probe", Vendor: string(vendor), OK: false, Message: "resolve BMC host: " + err.Error()})
		return SOLStream{}, err
	}
	port := proto.Port
	if port <= 0 {
		port = 22
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	add(CaptureDebugStep{
		Phase: "request", Vendor: string(vendor), Method: "SSH", URL: addr,
		Message: fmt.Sprintf("dial SSH SOL (Redfish-advertised; console_entry_command=%q)", proto.ConsoleEntryCommand),
	})

	sshCfg := &ssh.ClientConfig{
		User: c.cfg.Username,
		Auth: []ssh.AuthMethod{ssh.Password(c.cfg.Password)},
		// BMC SSH host keys are not pinned by operators today; this is a
		// documented limitation (see docs/real-hardware-sol-runbook.md), not an
		// oversight — Redfish itself is what authorized using SSH here.
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec
		Timeout:         10 * time.Second,
	}

	dialer := &net.Dialer{Timeout: sshCfg.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		add(CaptureDebugStep{Phase: "request", Vendor: string(vendor), Method: "SSH", URL: addr, OK: false, Message: err.Error()})
		return SOLStream{}, fmt.Errorf("redfish: ssh sol dial: %w", err)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, sshCfg)
	if err != nil {
		_ = conn.Close()
		add(CaptureDebugStep{Phase: "request", Vendor: string(vendor), Method: "SSH", URL: addr, OK: false, Message: err.Error()})
		return SOLStream{}, fmt.Errorf("redfish: ssh sol handshake: %w", err)
	}
	cl := ssh.NewClient(sshConn, chans, reqs)
	session, err := cl.NewSession()
	if err != nil {
		_ = cl.Close()
		add(CaptureDebugStep{Phase: "request", Vendor: string(vendor), Method: "SSH", URL: addr, OK: false, Message: err.Error()})
		return SOLStream{}, fmt.Errorf("redfish: ssh sol session: %w", err)
	}
	if err := session.RequestPty("vt100", 24, 80, ssh.TerminalModes{}); err != nil {
		_ = session.Close()
		_ = cl.Close()
		add(CaptureDebugStep{Phase: "request", Vendor: string(vendor), Method: "SSH", URL: addr, OK: false, Message: "request pty: " + err.Error()})
		return SOLStream{}, fmt.Errorf("redfish: ssh sol pty: %w", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		_ = cl.Close()
		return SOLStream{}, fmt.Errorf("redfish: ssh sol stdout: %w", err)
	}

	if cmd := strings.TrimSpace(proto.ConsoleEntryCommand); cmd != "" {
		err = session.Start(cmd)
	} else {
		err = session.Shell()
	}
	if err != nil {
		_ = session.Close()
		_ = cl.Close()
		add(CaptureDebugStep{Phase: "request", Vendor: string(vendor), Method: "SSH", URL: addr, OK: false, Message: "start console: " + err.Error()})
		return SOLStream{}, fmt.Errorf("redfish: ssh sol start: %w", err)
	}
	add(CaptureDebugStep{Phase: "request", Vendor: string(vendor), Method: "SSH", URL: addr, OK: true, Message: "ssh sol session started"})

	return SOLStream{
		ReadCloser: &sshSOLReadCloser{stdout: stdout, session: session, client: cl},
		Vendor:     vendor,
		Kind:       SOLConnectSSH,
	}, nil
}

func sshHost(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse BaseURL: %w", err)
	}
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("BaseURL %q has no host", base)
	}
	return host, nil
}

// sshSOLReadCloser adapts an ssh.Session's stdout + owning session/client into
// a single io.ReadCloser so Close() releases the whole SSH connection.
type sshSOLReadCloser struct {
	stdout  io.Reader
	session *ssh.Session
	client  *ssh.Client
}

func (r *sshSOLReadCloser) Read(p []byte) (int, error) { return r.stdout.Read(p) }

func (r *sshSOLReadCloser) Close() error {
	_ = r.session.Close()
	return r.client.Close()
}
