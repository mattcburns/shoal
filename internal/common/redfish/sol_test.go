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
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/coder/websocket"
)

// --- fake Redfish HTTP server (drives real gofish parsing, not a mock of *client) ---

type fakeSOLServerOpts struct {
	systemJSON  string // body for /redfish/v1/Systems/1
	managerJSON string // body for /redfish/v1/Managers/1
	wsPaths     []string
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
	for _, p := range opts.wsPaths {
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "")
			_ = conn.Write(r.Context(), websocket.MessageText, []byte("SHOAL|1|1|2026-07-19T00:00:00Z|BOOT|1|OK|ws-hello\n"))
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

func systemJSONWithHostSerialConsole(ssh, telnet, ipmi bool, sshPort int) string {
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
			"SSH": {"ServiceEnabled": %s, "Port": %d},
			"Telnet": {"ServiceEnabled": %s, "Port": 23},
			"IPMI": {"ServiceEnabled": %s, "Port": 623}
		}
	}`, b(ssh), sshPort, b(telnet), b(ipmi))
}

const systemJSONDellNoConsole = `{
	"@odata.id": "/redfish/v1/Systems/1",
	"Id": "1",
	"Name": "System.1",
	"Manufacturer": "Dell Inc.",
	"Model": "PowerEdge R640",
	"PowerState": "On"
}`

// --- fake SSH server (proves the Redfish-advertised SSH fallback end to end) ---

func startFakeSSHServer(t *testing.T, user, pass string, lines []string) int {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if conn.User() == user && string(password) == pass {
				return nil, nil
			}
			return nil, fmt.Errorf("fake ssh: bad credentials")
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			nConn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveFakeSSHConn(nConn, cfg, lines)
		}
	}()

	return ln.Addr().(*net.TCPAddr).Port
}

func serveFakeSSHConn(nConn net.Conn, cfg *ssh.ServerConfig, lines []string) {
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
			for req := range in {
				switch req.Type {
				case "pty-req":
					_ = req.Reply(true, nil)
				case "shell", "exec":
					_ = req.Reply(true, nil)
					go func() {
						for _, l := range lines {
							_, _ = io.WriteString(ch, l+"\n")
						}
						_ = ch.Close()
					}()
				default:
					_ = req.Reply(false, nil)
				}
			}
		}(requests, channel)
	}
}

// --- tests ---

func TestOpenSOL_SSHAdvertised_Success(t *testing.T) {
	sshLines := []string{"SHOAL|1|1|2026-07-19T00:00:00Z|BOOT|1|OK|ssh-hello"}
	sshPort := startFakeSSHServer(t, "admin", "password", sshLines)

	srv := newFakeSOLServer(t, fakeSOLServerOpts{
		systemJSON: systemJSONWithHostSerialConsole(true, false, false, sshPort),
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
}

func TestOpenSOL_IPMIOnly_Unsupported(t *testing.T) {
	srv := newFakeSOLServer(t, fakeSOLServerOpts{
		systemJSON: systemJSONWithHostSerialConsole(false, false, true, 623),
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
}

func TestOpenSOL_TelnetOnly_Unsupported(t *testing.T) {
	srv := newFakeSOLServer(t, fakeSOLServerOpts{
		systemJSON: systemJSONWithHostSerialConsole(false, true, false, 23),
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
		systemJSON: systemJSONWithHostSerialConsole(false, false, true, 623),
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
