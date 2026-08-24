package redfish

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	gofishredfish "github.com/stmcginnis/gofish/redfish"
	"golang.org/x/crypto/ssh"
)

type sshHint struct {
	eligible bool
	port     int
	entryCmd string
	reason   string
}

func (c *client) sshEligible(sys *gofishredfish.ComputerSystem, managers []*gofishredfish.Manager, vendor VendorID, add func(CaptureDebugStep)) sshHint {
	h := sshHint{port: 22, entryCmd: strings.TrimSpace(sys.SerialConsole.SSH.ConsoleEntryCommand)}
	if p := sys.SerialConsole.SSH.Port; p > 0 {
		h.port = p
	}

	if sys.SerialConsole.SSH.ServiceEnabled {
		h.eligible = true
		h.reason = "system SerialConsole.SSH.ServiceEnabled"
	}
	for _, m := range managers {
		if m == nil {
			continue
		}
		if m.SerialConsole.ServiceEnabled {
			for _, ct := range m.SerialConsole.ConnectTypesSupported {
				if strings.EqualFold(string(ct), string(gofishredfish.SSHSerialConnectTypesSupported)) {
					h.eligible = true
					if h.reason == "" {
						h.reason = "manager SerialConsole ConnectTypesSupported=SSH"
					}
				}
			}
		}
	}
	if vendor == VendorDell {
		if port, ok := c.dellNetworkProtocolSSH(managers, add); ok {
			h.eligible = true
			if h.reason == "" {
				h.reason = "Dell NetworkProtocol.SSH.ProtocolEnabled"
			}
			if h.port == 22 && port > 0 {
				h.port = port
			}
		}
		if c.dellSerialOEMEnabled(managers, add) {
			h.eligible = true
			if h.reason == "" {
				h.reason = "Dell OEM serial-redirection attributes Enabled"
			}
		}
	}
	if h.entryCmd != "" && !h.eligible {
		h.eligible = true
		h.reason = "ConsoleEntryCommand present"
	}
	if h.eligible {
		add(CaptureDebugStep{Phase: "probe", Vendor: string(vendor), OK: true, Message: "ssh eligible: " + h.reason + fmt.Sprintf(" port=%d", h.port)})
	} else {
		add(CaptureDebugStep{Phase: "probe", Vendor: string(vendor), OK: false, Message: "ssh ineligible"})
	}
	return h
}

func (c *client) dellNetworkProtocolSSH(managers []*gofishredfish.Manager, add func(CaptureDebugStep)) (port int, ok bool) {
	for _, m := range managers {
		if m == nil {
			continue
		}
		np, err := m.NetworkProtocol()
		if err != nil || np == nil {
			add(CaptureDebugStep{Phase: "probe", Vendor: string(VendorDell), OK: false, Message: "NetworkProtocol: not enabled (" + errString(err) + ")"})
			continue
		}
		if np.SSH.ProtocolEnabled {
			p := int(np.SSH.Port)
			add(CaptureDebugStep{Phase: "probe", Vendor: string(VendorDell), OK: true, Message: fmt.Sprintf("NetworkProtocol.SSH enabled port=%d", p)})
			return p, true
		}
	}
	return 0, false
}

func (c *client) dellSerialOEMEnabled(managers []*gofishredfish.Manager, add func(CaptureDebugStep)) bool {
	api, err := c.apiClient()
	if err != nil {
		return false
	}
	keys := []string{"serialredirection.1.enable", "ipmisol.1.enable", "ssh.1.enable"}
	for _, m := range managers {
		if m == nil {
			continue
		}
		base := strings.TrimSuffix(m.ODataID, "/")
		urls := []string{
			base + "/Oem/Dell/DellAttributes/iDRAC.Embedded.1",
			base + "/Attributes",
		}
		for _, u := range urls {
			resp, gerr := api.Get(u)
			if gerr != nil {
				add(CaptureDebugStep{Phase: "probe", Vendor: string(VendorDell), URL: u, OK: false, Message: "oem attributes: " + gerr.Error()})
				continue
			}
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			_ = resp.Body.Close()
			if resp.StatusCode == 404 {
				add(CaptureDebugStep{Phase: "probe", Vendor: string(VendorDell), URL: u, StatusCode: 404, OK: false, Message: "oem attributes 404"})
				continue
			}
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				add(CaptureDebugStep{Phase: "probe", Vendor: string(VendorDell), URL: u, StatusCode: resp.StatusCode, OK: false, Message: "oem attributes HTTP"})
				continue
			}
			var payload struct {
				Attributes map[string]any `json:"Attributes"`
			}
			if json.Unmarshal(body, &payload) != nil || payload.Attributes == nil {
				continue
			}
			for k, v := range payload.Attributes {
				lk := strings.ToLower(k)
				for _, want := range keys {
					if lk == want && strings.EqualFold(fmt.Sprint(v), "Enabled") {
						add(CaptureDebugStep{Phase: "probe", Vendor: string(VendorDell), URL: u, OK: true, Message: "oem " + k + "=Enabled"})
						return true
					}
				}
			}
		}
	}
	return false
}

func errString(err error) string {
	if err == nil {
		return "empty"
	}
	return err.Error()
}

func (c *client) trySSHSOL(ctx context.Context, vendor VendorID, hint sshHint, add func(CaptureDebugStep)) (SOLStream, error) {
	host, err := sshHost(c.cfg.BaseURL)
	if err != nil {
		add(CaptureDebugStep{Phase: "probe", Vendor: string(vendor), OK: false, Message: "resolve BMC host: " + err.Error()})
		return SOLStream{}, err
	}
	port := hint.port
	if port <= 0 {
		port = 22
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	add(CaptureDebugStep{
		Phase: "request", Vendor: string(vendor), Method: "SSH", URL: addr,
		Message: fmt.Sprintf("dial SSH SOL (%s; console_entry_command=%q)", hint.reason, hint.entryCmd),
	})

	password := c.cfg.Password
	sshCfg := &ssh.ClientConfig{
		User: c.cfg.Username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
			// iDRAC (and many BMCs) advertise keyboard-interactive, not password.
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range questions {
					answers[i] = password
				}
				return answers, nil
			}),
		},
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

	openSess := func() (*ssh.Session, io.Reader, io.WriteCloser, error) {
		session, serr := cl.NewSession()
		if serr != nil {
			return nil, nil, nil, serr
		}
		if perr := session.RequestPty("vt100", 24, 80, ssh.TerminalModes{}); perr != nil {
			_ = session.Close()
			return nil, nil, nil, fmt.Errorf("pty: %w", perr)
		}
		stdout, oerr := session.StdoutPipe()
		if oerr != nil {
			_ = session.Close()
			return nil, nil, nil, oerr
		}
		if stderr, eerr := session.StderrPipe(); eerr == nil {
			go func() { _, _ = io.Copy(io.Discard, stderr) }()
		}
		stdin, ierr := session.StdinPipe()
		if ierr != nil {
			_ = session.Close()
			return nil, nil, nil, ierr
		}
		return session, stdout, stdin, nil
	}

	startCmd := func(cmd string) (*ssh.Session, io.Reader, io.WriteCloser, error) {
		session, stdout, stdin, oerr := openSess()
		if oerr != nil {
			return nil, nil, nil, oerr
		}
		if serr := session.Start(cmd); serr != nil {
			_ = session.Close()
			return nil, nil, nil, serr
		}
		return session, stdout, stdin, nil
	}

	var (
		session *ssh.Session
		stdout  io.Reader
		stdin   io.WriteCloser
	)

	if cmd := strings.TrimSpace(hint.entryCmd); cmd != "" {
		session, stdout, stdin, err = startCmd(cmd)
	} else if vendor == VendorDell {
		session, stdout, stdin, err = startCmd("console com2")
		if err != nil {
			if session != nil {
				_ = session.Close()
			}
			add(CaptureDebugStep{Phase: "probe", Vendor: string(vendor), Method: "SSH", URL: addr, OK: false, Message: "console com2 rejected; trying connect"})
			session, stdout, stdin, err = startCmd("connect")
		}
		if err != nil {
			if session != nil {
				_ = session.Close()
			}
			add(CaptureDebugStep{Phase: "probe", Vendor: string(vendor), Method: "SSH", URL: addr, OK: false, Message: "connect failed; trying shell + write"})
			session, stdout, stdin, err = openSess()
			if err == nil {
				err = session.Shell()
			}
			if err == nil && stdin != nil {
				_, _ = io.WriteString(stdin, "console com2\r\nconnect\r\n")
			}
		}
	} else {
		_ = cl.Close()
		add(CaptureDebugStep{Phase: "probe", Vendor: string(vendor), OK: false, Message: "ssh: no ConsoleEntryCommand; vendor attach not implemented"})
		return SOLStream{}, fmt.Errorf("redfish: ssh sol: no ConsoleEntryCommand; vendor attach not implemented")
	}
	if err != nil {
		if session != nil {
			_ = session.Close()
		}
		_ = cl.Close()
		add(CaptureDebugStep{Phase: "request", Vendor: string(vendor), Method: "SSH", URL: addr, OK: false, Message: "start console: " + err.Error()})
		return SOLStream{}, fmt.Errorf("redfish: ssh sol start: %w", err)
	}
	add(CaptureDebugStep{Phase: "request", Vendor: string(vendor), Method: "SSH", URL: addr, OK: true, Message: "ssh sol session started"})

	return SOLStream{
		ReadCloser: &sshSOLReadCloser{stdout: stdout, stdin: stdin, session: session, client: cl},
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
// a single io.ReadCloser so Close() detaches Dell SOL then releases SSH.
type sshSOLReadCloser struct {
	stdout  io.Reader
	stdin   io.WriteCloser
	session *ssh.Session
	client  *ssh.Client
}

func (r *sshSOLReadCloser) Read(p []byte) (int, error) { return r.stdout.Read(p) }

func (r *sshSOLReadCloser) Close() error {
	if r.stdin != nil {
		_, _ = r.stdin.Write([]byte("\r\x1c."))
		time.Sleep(50 * time.Millisecond)
		_, _ = r.stdin.Write([]byte("\x1d."))
		_ = r.stdin.Close()
	}
	_ = r.session.Close()
	return r.client.Close()
}
