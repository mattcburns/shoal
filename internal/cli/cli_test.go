package cli_test

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/cli"
)

func TestVersion(t *testing.T) {
	// cmdVersion writes to stdout via Run only for "version" — use Run with captured stdout.
	// Run uses os.Stdout directly for version; test the Version var and binary instead.
	if cli.Version == "" {
		t.Fatal("empty version")
	}
}

func TestServeHealthzIntegration(t *testing.T) {
	// Build binary once for a black-box serve smoke (stdlib only).
	bin := filepath.Join(t.TempDir(), "shoal")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/mattcburns/shoal/cmd/shoal")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	serve := exec.Command(bin, "serve", "-addr", addr)
	serve.Env = append(os.Environ(), "SHOAL_LOG_LEVEL=error")
	stderr, _ := serve.StderrPipe()
	if err := serve.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = serve.Process.Kill()
		_, _ = serve.Process.Wait()
	}()

	// Wait until listening
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != 200 {
				t.Fatalf("healthz status %d body %s", resp.StatusCode, body)
			}
			if !bytes.Contains(body, []byte(`"status"`)) {
				t.Fatalf("body %s", body)
			}
			// version subcommand
			vOut, err := exec.Command(bin, "version").Output()
			if err != nil {
				t.Fatal(err)
			}
			if len(bytes.TrimSpace(vOut)) == 0 {
				t.Fatal("empty version output")
			}
			return
		}
		lastErr = err
		time.Sleep(50 * time.Millisecond)
	}
	// drain stderr for diagnosis
	errBytes, _ := io.ReadAll(stderr)
	t.Fatalf("server never became ready: %v\nstderr=%s", lastErr, errBytes)
}
