package redfish

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/coder/websocket"

	"github.com/mattcburns/shoal/internal/common/redfish/internal/ipmi"
)

func TestMain(m *testing.M) {
	ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		panic(err)
	}
	ipmiPort = ln.LocalAddr().(*net.UDPAddr).Port
	ipmiTimeout = 50 * time.Millisecond
	code := m.Run()
	_ = ln.Close()
	os.Exit(code)
}

// --- fake Redfish HTTP server (drives real gofish parsing, not a mock of *client) ---

type fakeSOLServerOpts struct {
	systemJSON          string // body for /redfish/v1/Systems/1
	managerJSON         string // body for /redfish/v1/Managers/1
	networkProtocolJSON string // body for /redfish/v1/Managers/1/NetworkProtocol
	oemAttributesJSON   string // body for Dell OEM Attributes
	wsPaths             []string
	wsPayload           []byte                // default: SHOAL|…ws-hello
	wsMessageType       websocket.MessageType // 0 → text
}

func newFakeSOLServer(t *testing.T, opts fakeSOLServerOpts) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/redfish/v1/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"@odata.id": "/redfish/v1/",
			"Id": "RootService",
			"Name": "Root Service",
			"RedfishVersion": "1.9.0",
			"Systems": {"@odata.id": "/redfish/v1/Systems"},
			"Managers": {"@odata.id": "/redfish/v1/Managers"}
		}`)
	})
	mux.HandleFunc("/redfish/v1/Systems", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"@odata.id": "/redfish/v1/Systems",
			"Name": "Systems Collection",
			"Members@odata.count": 1,
			"Members": [{"@odata.id": "/redfish/v1/Systems/1"}]
		}`)
	})
	mux.HandleFunc("/redfish/v1/Systems/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, opts.systemJSON)
	})
	mux.HandleFunc("/redfish/v1/Managers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"@odata.id": "/redfish/v1/Managers",
			"Name": "Managers Collection",
			"Members@odata.count": 1,
			"Members": [{"@odata.id": "/redfish/v1/Managers/1"}]
		}`)
	})
	mux.HandleFunc("/redfish/v1/Managers/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := opts.managerJSON
		if body == "" {
			body = `{"@odata.id": "/redfish/v1/Managers/1", "Id": "1", "Name": "Manager"}`
		}
		_, _ = io.WriteString(w, body)
	})
	mux.HandleFunc("/redfish/v1/Managers/1/NetworkProtocol", func(w http.ResponseWriter, r *http.Request) {
		if opts.networkProtocolJSON == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, opts.networkProtocolJSON)
	})
	oemHandler := func(w http.ResponseWriter, r *http.Request) {
		if opts.oemAttributesJSON == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, opts.oemAttributesJSON)
	}
	mux.HandleFunc("/redfish/v1/Managers/1/Oem/Dell/DellAttributes/iDRAC.Embedded.1", oemHandler)
	mux.HandleFunc("/redfish/v1/Managers/1/Attributes", oemHandler)
	wsType := opts.wsMessageType
	if wsType == 0 {
		wsType = websocket.MessageText
	}
	wsPayload := opts.wsPayload
	if wsPayload == nil {
		wsPayload = []byte("SHOAL|1|1|2026-07-19T00:00:00Z|BOOT|1|OK|ws-hello\n")
	}
	for _, p := range opts.wsPaths {
		path := p
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "")
			_ = conn.Write(r.Context(), wsType, wsPayload)
			// Keep reading so the client's close-handshake frame is answered
			// promptly instead of forcing the client to wait out its close timeout.
			for {
				if _, _, err := conn.Read(r.Context()); err != nil {
					return
				}
			}
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func openFakeClient(t *testing.T, baseURL string) *client {
	t.Helper()
	bmc, err := NewBMC(Config{BaseURL: baseURL, Username: "admin", Password: "password", AuthMode: "basic"})
	if err != nil {
		t.Fatal(err)
	}
	c := bmc.(*client)
	if err := c.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

const systemJSONNoConsole = `{
	"@odata.id": "/redfish/v1/Systems/1",
	"Id": "1",
	"Name": "System.1",
	"Manufacturer": "Shoal Virtual",
	"Model": "sushy",
	"PowerState": "On"
}`

func systemJSONWithHostSerialConsole(ssh, telnet, ipmi bool, sshPort int, entryCmd string) string {
	b := func(v bool) string {
		if v {
			return "true"
		}
		return "false"
	}
	return fmt.Sprintf(`{
		"@odata.id": "/redfish/v1/Systems/1",
		"Id": "1",
		"Name": "System.1",
		"Manufacturer": "Shoal Virtual",
		"Model": "sushy",
		"PowerState": "On",
		"SerialConsole": {
			"MaxConcurrentSessions": 1,
			"SSH": {"ServiceEnabled": %s, "Port": %d, "ConsoleEntryCommand": %q},
			"Telnet": {"ServiceEnabled": %s, "Port": 23},
			"IPMI": {"ServiceEnabled": %s, "Port": 623}
		}
	}`, b(ssh), sshPort, entryCmd, b(telnet), b(ipmi))
}

func managerJSONWithNetworkProtocol() string {
	return `{
		"@odata.id": "/redfish/v1/Managers/1",
		"Id": "1",
		"Name": "Manager",
		"NetworkProtocol": {"@odata.id": "/redfish/v1/Managers/1/NetworkProtocol"}
	}`
}

func networkProtocolJSON(sshEnabled bool, sshPort int) string {
	b := "false"
	if sshEnabled {
		b = "true"
	}
	return fmt.Sprintf(`{
		"@odata.id": "/redfish/v1/Managers/1/NetworkProtocol",
		"Id": "NetworkProtocol",
		"Name": "Manager Network Protocol",
		"SSH": {"ProtocolEnabled": %s, "Port": %d}
	}`, b, sshPort)
}

func systemJSONDell(powerState string) string {
	return fmt.Sprintf(`{
		"@odata.id": "/redfish/v1/Systems/1",
		"Id": "1",
		"Name": "System.1",
		"Manufacturer": "Dell Inc.",
		"Model": "PowerEdge R750",
		"PowerState": %q
	}`, powerState)
}

const systemJSONDellNoConsole = `{
	"@odata.id": "/redfish/v1/Systems/1",
	"Id": "1",
	"Name": "System.1",
	"Manufacturer": "Dell Inc.",
	"Model": "PowerEdge R640",
	"PowerState": "On"
}`

// --- fake SSH server (proves Redfish-advertised and Dell NetworkProtocol attach) ---

type fakeSSHRecorder struct {
	mu     sync.Mutex
	Execs  []string
	Shells int
	Stdin  []byte
}

func (r *fakeSSHRecorder) addExec(cmd string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.Execs = append(r.Execs, cmd)
	r.mu.Unlock()
}

func (r *fakeSSHRecorder) addShell() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.Shells++
	r.mu.Unlock()
}

func (r *fakeSSHRecorder) addStdin(p []byte) {
	if r == nil || len(p) == 0 {
		return
	}
	r.mu.Lock()
	r.Stdin = append(r.Stdin, p...)
	r.mu.Unlock()
}

func (r *fakeSSHRecorder) execs() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.Execs))
	copy(out, r.Execs)
	return out
}

func (r *fakeSSHRecorder) stdin() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.Stdin)
}

type fakeSSHOptions struct {
	user, pass     string
	lines          []string
	rejectExecOnce string // Reply(false) the first time this exec command is seen
	keepOpen       bool   // do not close the channel after writing lines (Close/stdin tests)
	// keyboardInteractive: advertise only keyboard-interactive (iDRAC shape).
	keyboardInteractive bool
	rec                 *fakeSSHRecorder
}

func startFakeSSHServer(t *testing.T, opts fakeSSHOptions) int {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{}
	if opts.keyboardInteractive {
		cfg.KeyboardInteractiveCallback = func(conn ssh.ConnMetadata, client ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			ans, err := client("", "", []string{"Password: "}, []bool{false})
			if err != nil {
				return nil, err
			}
			if conn.User() == opts.user && len(ans) > 0 && ans[0] == opts.pass {
				return nil, nil
			}
			return nil, fmt.Errorf("fake ssh: bad credentials")
		}
	} else {
		cfg.PasswordCallback = func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if conn.User() == opts.user && string(password) == opts.pass {
				return nil, nil
			}
			return nil, fmt.Errorf("fake ssh: bad credentials")
		}
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	var rejectOnce sync.Once

	go func() {
		for {
			nConn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveFakeSSHConn(nConn, cfg, opts, &rejectOnce)
		}
	}()

	return ln.Addr().(*net.TCPAddr).Port
}

func serveFakeSSHConn(nConn net.Conn, cfg *ssh.ServerConfig, opts fakeSSHOptions, rejectOnce *sync.Once) {
	sConn, chans, reqs, err := ssh.NewServerConn(nConn, cfg)
	if err != nil {
		return
	}
	defer sConn.Close()
	go ssh.DiscardRequests(reqs)
	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "unsupported channel type")
			continue
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go func(in <-chan *ssh.Request, ch ssh.Channel) {
			go func() {
				buf := make([]byte, 256)
				for {
					n, rerr := ch.Read(buf)
					if n > 0 {
						opts.rec.addStdin(buf[:n])
					}
					if rerr != nil {
						return
					}
				}
			}()
			started := false
			for req := range in {
				switch req.Type {
				case "pty-req":
					_ = req.Reply(true, nil)
				case "exec":
					var msg struct{ Command string }
					_ = ssh.Unmarshal(req.Payload, &msg)
					opts.rec.addExec(msg.Command)
					reject := false
					if opts.rejectExecOnce != "" && msg.Command == opts.rejectExecOnce {
						rejectOnce.Do(func() { reject = true })
						if reject {
							_ = req.Reply(false, nil)
							continue
						}
					}
					_ = req.Reply(true, nil)
					if !started {
						started = true
						go writeFakeSSHLines(ch, opts.lines, opts.keepOpen)
					}
				case "shell":
					opts.rec.addShell()
					_ = req.Reply(true, nil)
					if !started {
						started = true
						go writeFakeSSHLines(ch, opts.lines, opts.keepOpen)
					}
				default:
					_ = req.Reply(false, nil)
				}
			}
		}(requests, channel)
	}
}

func writeFakeSSHLines(ch ssh.Channel, lines []string, keepOpen bool) {
	for _, l := range lines {
		_, _ = io.WriteString(ch, l+"\n")
	}
	if !keepOpen {
		_ = ch.Close()
	}
}

// --- tests ---

func TestOpenSOL_SSHAdvertised_Success(t *testing.T) {
	sshLines := []string{"SHOAL|1|1|2026-07-19T00:00:00Z|BOOT|1|OK|ssh-hello"}
	rec := &fakeSSHRecorder{}
	sshPort := startFakeSSHServer(t, fakeSSHOptions{
		user: "admin", pass: "password", lines: sshLines, rec: rec,
	})

	srv := newFakeSOLServer(t, fakeSOLServerOpts{
		systemJSON: systemJSONWithHostSerialConsole(true, false, false, sshPort, "sol-attach"),
	})
	c := openFakeClient(t, srv.URL)

	stream, err := c.OpenSOL(context.Background(), "1")
	if err != nil {
		t.Fatalf("OpenSOL: %v", err)
	}
	defer stream.Close()
	if stream.Kind != SOLConnectSSH {
		t.Fatalf("kind = %q, want ssh", stream.Kind)
	}

	buf := make([]byte, 4096)
	n, _ := io.ReadFull(stream, buf[:len(sshLines[0])+1])
	got := string(buf[:n])
	if !strings.Contains(got, "ssh-hello") {
		t.Fatalf("stream content = %q, want it to contain %q", got, "ssh-hello")
	}
	if got := rec.execs(); len(got) != 1 || got[0] != "sol-attach" {
		t.Fatalf("execs = %v, want [sol-attach]", got)
	}
}

func TestOpenSOL_IPMIOnly_Unsupported(t *testing.T) {
	srv := newFakeSOLServer(t, fakeSOLServerOpts{
		systemJSON: systemJSONWithHostSerialConsole(false, false, true, 623, ""),
	})
	c := openFakeClient(t, srv.URL)

	_, err := c.OpenSOL(context.Background(), "1")
	if err == nil {
		t.Fatal("expected unsupported error for IPMI-only BMC")
	}
	var unsupported *SOLUnsupportedError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected *SOLUnsupportedError, got %T: %v", err, err)
	}
	found := false
	for _, ct := range unsupported.ConnectTypes {
		if ct == "IPMI" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ConnectTypes = %v, want it to include IPMI", unsupported.ConnectTypes)
	}
	trail := err.Error()
	if !strings.Contains(trail, "udp 623") && !strings.Contains(trail, "ipmi") {
		t.Fatalf("error = %q, want ipmi/udp timeout in debug trail", trail)
	}
	if strings.Contains(trail, "cipher suite 17") {
		t.Fatal("must not try suite 17 after Get Channel Auth Caps timeout")
	}
}

func TestOpenSOL_IPMI_Success(t *testing.T) {
	bmc, err := ipmi.StartTestBMC(ipmi.TestBMCOptions{Username: "admin", Password: "password", Greet: []byte("ipmi-hello\n")})
	if err != nil {
		t.Fatal(err)
	}
	defer bmc.Close()
	oldPort, oldTO := ipmiPort, ipmiTimeout
	ipmiPort = bmc.Addr.Port
	ipmiTimeout = 500 * time.Millisecond
	t.Cleanup(func() { ipmiPort, ipmiTimeout = oldPort, oldTO })

	srv := newFakeSOLServer(t, fakeSOLServerOpts{
		systemJSON: systemJSONWithHostSerialConsole(false, false, true, 623, ""),
	})
	c := openFakeClient(t, srv.URL)
	stream, err := c.OpenSOL(context.Background(), "1")
	if err != nil {
		t.Fatalf("OpenSOL: %v", err)
	}
	defer stream.Close()
	if stream.Kind != SOLConnectIPMI {
		t.Fatalf("kind = %q, want ipmi", stream.Kind)
	}
	buf := make([]byte, 64)
	n, rerr := stream.Read(buf)
	if rerr != nil && n == 0 {
		t.Fatalf("read: %v", rerr)
	}
	if !strings.Contains(string(buf[:n]), "ipmi-hello") {
		t.Fatalf("stream = %q, want ipmi-hello", buf[:n])
	}
}

func TestOpenSOL_TelnetOnly_Unsupported(t *testing.T) {
	srv := newFakeSOLServer(t, fakeSOLServerOpts{
		systemJSON: systemJSONWithHostSerialConsole(false, true, false, 23, ""),
	})
	c := openFakeClient(t, srv.URL)

	_, err := c.OpenSOL(context.Background(), "1")
	if err == nil {
		t.Fatal("expected unsupported error for Telnet-only BMC (deferred, not implemented)")
	}
	var unsupported *SOLUnsupportedError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected *SOLUnsupportedError, got %T: %v", err, err)
	}
}

func TestOpenSOL_NoConsole_Unsupported(t *testing.T) {
	srv := newFakeSOLServer(t, fakeSOLServerOpts{systemJSON: systemJSONNoConsole})
	c := openFakeClient(t, srv.URL)

	_, err := c.OpenSOL(context.Background(), "1")
	var unsupported *SOLUnsupportedError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected *SOLUnsupportedError, got %T: %v", err, err)
	}
	if unsupported.Vendor != VendorUnknown {
		t.Fatalf("vendor = %q, want unknown", unsupported.Vendor)
	}
}

func TestOpenSOL_WebSocketDell_Success(t *testing.T) {
	srv := newFakeSOLServer(t, fakeSOLServerOpts{
		systemJSON: systemJSONDellNoConsole,
		wsPaths:    []string{"/console"},
	})
	c := openFakeClient(t, srv.URL)

	stream, err := c.OpenSOL(context.Background(), "1")
	if err != nil {
		t.Fatalf("OpenSOL: %v", err)
	}
	defer stream.Close()
	if stream.Kind != SOLConnectWebSocket {
		t.Fatalf("kind = %q, want websocket", stream.Kind)
	}
	if stream.Vendor != VendorDell {
		t.Fatalf("vendor = %q, want dell", stream.Vendor)
	}

	buf := make([]byte, 256)
	n, err := stream.Read(buf)
	if err != nil && n == 0 {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(buf[:n]), "ws-hello") {
		t.Fatalf("stream content = %q, want it to contain ws-hello", string(buf[:n]))
	}
}

func TestOpenSOL_WebSocketCandidatesFail_Unsupported(t *testing.T) {
	// Dell vendor but no WS handler registered at any candidate path (404s),
	// and no SSH advertised either -> unsupported, proving the fallthrough.
	srv := newFakeSOLServer(t, fakeSOLServerOpts{systemJSON: systemJSONDellNoConsole})
	c := openFakeClient(t, srv.URL)

	_, err := c.OpenSOL(context.Background(), "1")
	var unsupported *SOLUnsupportedError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected *SOLUnsupportedError, got %T: %v", err, err)
	}
	if unsupported.Vendor != VendorDell {
		t.Fatalf("vendor = %q, want dell", unsupported.Vendor)
	}
}

func TestOpenSOL_DebugTrailRedactsSecrets(t *testing.T) {
	srv := newFakeSOLServer(t, fakeSOLServerOpts{
		systemJSON: systemJSONWithHostSerialConsole(false, false, true, 623, ""),
	})
	c := openFakeClient(t, srv.URL)

	_, err := c.OpenSOL(context.Background(), "1")
	var unsupported *SOLUnsupportedError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected *SOLUnsupportedError, got %T: %v", err, err)
	}
	for _, step := range unsupported.Debug {
		if strings.Contains(strings.ToLower(step.Message), "password") ||
			strings.Contains(strings.ToLower(step.BodyPreview), "password") {
			t.Fatalf("debug step leaked a password-like value: %+v", step)
		}
	}
	if !strings.Contains(err.Error(), "IPMI") {
		t.Fatalf("error text = %q, want it to mention observed IPMI connect type", err.Error())
	}
}

func TestOpenSOL_DellEmptySerialConsole_SSHAttach(t *testing.T) {
	sshLines := []string{"SHOAL|1|1|2026-07-19T00:00:00Z|BOOT|1|OK|dell-com2"}
	rec := &fakeSSHRecorder{}
	sshPort := startFakeSSHServer(t, fakeSSHOptions{
		user: "admin", pass: "password", lines: sshLines, rec: rec,
	})
	srv := newFakeSOLServer(t, fakeSOLServerOpts{
		systemJSON:          systemJSONDell("On"),
		managerJSON:         managerJSONWithNetworkProtocol(),
		networkProtocolJSON: networkProtocolJSON(true, sshPort),
	})
	c := openFakeClient(t, srv.URL)

	stream, err := c.OpenSOL(context.Background(), "1")
	if err != nil {
		t.Fatalf("OpenSOL: %v", err)
	}
	defer stream.Close()
	if stream.Kind != SOLConnectSSH {
		t.Fatalf("kind = %q, want ssh", stream.Kind)
	}
	if stream.Vendor != VendorDell {
		t.Fatalf("vendor = %q, want dell", stream.Vendor)
	}
	buf := make([]byte, 4096)
	n, _ := io.ReadFull(stream, buf[:len(sshLines[0])+1])
	if !strings.Contains(string(buf[:n]), "dell-com2") {
		t.Fatalf("stream content = %q, want dell-com2", buf[:n])
	}
	if got := rec.execs(); len(got) != 1 || got[0] != "console com2" {
		t.Fatalf("execs = %v, want [console com2]", got)
	}
}

func TestOpenSOL_DellSSH_KeyboardInteractive(t *testing.T) {
	sshLines := []string{"SHOAL|1|1|2026-07-19T00:00:00Z|BOOT|1|OK|ki-hello"}
	rec := &fakeSSHRecorder{}
	sshPort := startFakeSSHServer(t, fakeSSHOptions{
		user: "admin", pass: "password", lines: sshLines, rec: rec,
		keyboardInteractive: true,
	})
	srv := newFakeSOLServer(t, fakeSOLServerOpts{
		systemJSON:          systemJSONDell("On"),
		managerJSON:         managerJSONWithNetworkProtocol(),
		networkProtocolJSON: networkProtocolJSON(true, sshPort),
	})
	c := openFakeClient(t, srv.URL)

	stream, err := c.OpenSOL(context.Background(), "1")
	if err != nil {
		t.Fatalf("OpenSOL: %v", err)
	}
	defer stream.Close()
	if stream.Kind != SOLConnectSSH {
		t.Fatalf("kind = %q, want ssh", stream.Kind)
	}
	buf := make([]byte, 4096)
	n, _ := io.ReadFull(stream, buf[:len(sshLines[0])+1])
	if !strings.Contains(string(buf[:n]), "ki-hello") {
		t.Fatalf("stream content = %q, want ki-hello", buf[:n])
	}
}

func TestOpenSOL_DellSSH_ConnectFallback(t *testing.T) {
	sshLines := []string{"SHOAL|1|1|2026-07-19T00:00:00Z|BOOT|1|OK|via-connect"}
	rec := &fakeSSHRecorder{}
	sshPort := startFakeSSHServer(t, fakeSSHOptions{
		user: "admin", pass: "password", lines: sshLines, rec: rec,
		rejectExecOnce: "console com2",
	})
	srv := newFakeSOLServer(t, fakeSOLServerOpts{
		systemJSON:          systemJSONDell("On"),
		managerJSON:         managerJSONWithNetworkProtocol(),
		networkProtocolJSON: networkProtocolJSON(true, sshPort),
	})
	c := openFakeClient(t, srv.URL)

	stream, err := c.OpenSOL(context.Background(), "1")
	if err != nil {
		t.Fatalf("OpenSOL: %v", err)
	}
	defer stream.Close()
	if stream.Kind != SOLConnectSSH {
		t.Fatalf("kind = %q, want ssh", stream.Kind)
	}
	buf := make([]byte, 4096)
	n, _ := io.ReadFull(stream, buf[:len(sshLines[0])+1])
	if !strings.Contains(string(buf[:n]), "via-connect") {
		t.Fatalf("stream content = %q, want via-connect", buf[:n])
	}
	if got := rec.execs(); len(got) != 2 || got[0] != "console com2" || got[1] != "connect" {
		t.Fatalf("execs = %v, want [console com2 connect]", got)
	}
}

func TestOpenSOL_HostOff_QuietStreamNotError(t *testing.T) {
	rec := &fakeSSHRecorder{}
	sshPort := startFakeSSHServer(t, fakeSSHOptions{
		user: "admin", pass: "password", keepOpen: true, rec: rec,
	})
	srv := newFakeSOLServer(t, fakeSOLServerOpts{
		systemJSON:          systemJSONDell("Off"),
		managerJSON:         managerJSONWithNetworkProtocol(),
		networkProtocolJSON: networkProtocolJSON(true, sshPort),
	})
	c := openFakeClient(t, srv.URL)

	stream, err := c.OpenSOL(context.Background(), "1")
	if err != nil {
		t.Fatalf("OpenSOL with PowerState=Off: %v", err)
	}
	defer stream.Close()
	if stream.Kind != SOLConnectSSH {
		t.Fatalf("kind = %q, want ssh", stream.Kind)
	}
	found := false
	for _, step := range stream.Debug {
		if strings.Contains(step.Message, "PowerState=Off") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("debug trail missing PowerState=Off quiet-stream note: %+v", stream.Debug)
	}
	if got := rec.execs(); len(got) != 1 || got[0] != "console com2" {
		t.Fatalf("execs = %v, want [console com2]", got)
	}
}

func TestOpenSOL_WSHTMLOrBinary_FallsThroughToSSH(t *testing.T) {
	sshLines := []string{"SHOAL|1|1|2026-07-19T00:00:00Z|BOOT|1|OK|after-kvm"}
	rec := &fakeSSHRecorder{}
	sshPort := startFakeSSHServer(t, fakeSSHOptions{
		user: "admin", pass: "password", lines: sshLines, rec: rec,
	})
	srv := newFakeSOLServer(t, fakeSOLServerOpts{
		systemJSON:          systemJSONDell("On"),
		managerJSON:         managerJSONWithNetworkProtocol(),
		networkProtocolJSON: networkProtocolJSON(true, sshPort),
		wsPaths:             []string{"/console"},
		wsPayload:           []byte("<!DOCTYPE html><html><body>kvm</body></html>"),
	})
	c := openFakeClient(t, srv.URL)

	stream, err := c.OpenSOL(context.Background(), "1")
	if err != nil {
		t.Fatalf("OpenSOL: %v", err)
	}
	defer stream.Close()
	if stream.Kind != SOLConnectSSH {
		t.Fatalf("kind = %q, want ssh (HTML websocket must fall through), got %q", stream.Kind, stream.Kind)
	}
	buf := make([]byte, 4096)
	n, _ := io.ReadFull(stream, buf[:len(sshLines[0])+1])
	if !strings.Contains(string(buf[:n]), "after-kvm") {
		t.Fatalf("first SOL line = %q, want it from SSH (after-kvm), not WS HTML", buf[:n])
	}
}

func TestOpenSOL_WSBinary_FallsThroughToSSH(t *testing.T) {
	sshLines := []string{"SHOAL|1|1|2026-07-19T00:00:00Z|BOOT|1|OK|after-binary"}
	rec := &fakeSSHRecorder{}
	sshPort := startFakeSSHServer(t, fakeSSHOptions{
		user: "admin", pass: "password", lines: sshLines, rec: rec,
	})
	srv := newFakeSOLServer(t, fakeSOLServerOpts{
		systemJSON:          systemJSONDell("On"),
		managerJSON:         managerJSONWithNetworkProtocol(),
		networkProtocolJSON: networkProtocolJSON(true, sshPort),
		wsPaths:             []string{"/console"},
		wsMessageType:       websocket.MessageBinary,
		wsPayload:           []byte{0x00, 0x01, 0xff, 0x80, 0x00},
	})
	c := openFakeClient(t, srv.URL)

	stream, err := c.OpenSOL(context.Background(), "1")
	if err != nil {
		t.Fatalf("OpenSOL: %v", err)
	}
	defer stream.Close()
	if stream.Kind != SOLConnectSSH {
		t.Fatalf("kind = %q, want ssh (binary websocket must fall through)", stream.Kind)
	}
	buf := make([]byte, 4096)
	n, _ := io.ReadFull(stream, buf[:len(sshLines[0])+1])
	if !strings.Contains(string(buf[:n]), "after-binary") {
		t.Fatalf("stream = %q, want after-binary from SSH", buf[:n])
	}
}

func TestOpenSOL_WSTextSOL_PrependsFirstFrame(t *testing.T) {
	srv := newFakeSOLServer(t, fakeSOLServerOpts{
		systemJSON: systemJSONDellNoConsole,
		wsPaths:    []string{"/console"},
	})
	c := openFakeClient(t, srv.URL)

	stream, err := c.OpenSOL(context.Background(), "1")
	if err != nil {
		t.Fatalf("OpenSOL: %v", err)
	}
	defer stream.Close()
	if stream.Kind != SOLConnectWebSocket {
		t.Fatalf("kind = %q, want websocket", stream.Kind)
	}
	buf := make([]byte, 256)
	n, err := stream.Read(buf)
	if err != nil && n == 0 {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(buf[:n]), "ws-hello") {
		t.Fatalf("stream content = %q, want prepended sniff frame with ws-hello", string(buf[:n]))
	}
}

func TestOpenSOL_NonDellNetworkProtocolOnly_NoConsoleCom2(t *testing.T) {
	rec := &fakeSSHRecorder{}
	sshPort := startFakeSSHServer(t, fakeSSHOptions{
		user: "admin", pass: "password",
		lines: []string{"should-not-see"},
		rec:   rec,
	})
	srv := newFakeSOLServer(t, fakeSOLServerOpts{
		systemJSON: `{
			"@odata.id": "/redfish/v1/Systems/1",
			"Id": "1",
			"Name": "System.1",
			"Manufacturer": "Super Micro Computer",
			"Model": "X11",
			"PowerState": "On"
		}`,
		managerJSON:         managerJSONWithNetworkProtocol(),
		networkProtocolJSON: networkProtocolJSON(true, sshPort),
	})
	c := openFakeClient(t, srv.URL)

	_, err := c.OpenSOL(context.Background(), "1")
	var unsupported *SOLUnsupportedError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected *SOLUnsupportedError, got %T: %v", err, err)
	}
	if unsupported.Vendor != VendorSupermicro {
		t.Fatalf("vendor = %q, want supermicro", unsupported.Vendor)
	}
	ineligible := false
	for _, step := range unsupported.Debug {
		if strings.Contains(step.Message, "ssh ineligible") {
			ineligible = true
		}
		if strings.Contains(step.Message, "console com2") {
			t.Fatalf("non-Dell must not guess console com2: %+v", step)
		}
	}
	if !ineligible {
		t.Fatalf("debug trail should record ssh ineligible; got %+v", unsupported.Debug)
	}
	if got := rec.execs(); len(got) != 0 {
		t.Fatalf("execs = %v, want none (must not Start console com2)", got)
	}
}

func TestOpenSOL_DellSSH_CloseWritesDetach(t *testing.T) {
	rec := &fakeSSHRecorder{}
	sshPort := startFakeSSHServer(t, fakeSSHOptions{
		user: "admin", pass: "password",
		lines:    []string{"attached"},
		keepOpen: true,
		rec:      rec,
	})
	srv := newFakeSOLServer(t, fakeSOLServerOpts{
		systemJSON:          systemJSONDell("On"),
		managerJSON:         managerJSONWithNetworkProtocol(),
		networkProtocolJSON: networkProtocolJSON(true, sshPort),
	})
	c := openFakeClient(t, srv.URL)

	stream, err := c.OpenSOL(context.Background(), "1")
	if err != nil {
		t.Fatalf("OpenSOL: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got := rec.stdin()
	if !strings.Contains(got, "\r\x1c.") {
		t.Fatalf("Close stdin = %q, want it to contain Dell detach \\r\\x1c.", got)
	}
}
