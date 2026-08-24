//go:build live_sol

package redfish_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mattcburns/shoal/internal/common/redfish"
)

// Live attach against a real BMC (operator-gated).
//
//	set -a && . ./.env && set +a
//	SHOAL_BMC_URL=https://172.16.21.202 \
//	  go test ./internal/common/redfish -tags=live_sol -run 'TestLiveOpenSOL' -v -count=1 -timeout 4m
//
// TestLiveOpenSOL is attach-only (no power). TestLiveOpenSOL_ResetAndRead
// attaches first, then On / ForceRestart, and records console text.

func TestLiveOpenSOL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	bmc, sys := liveBMC(t, ctx)
	defer func() { _ = bmc.Close(context.Background()) }()
	stream := liveOpenSOL(t, ctx, bmc, sys.ID)
	defer closeStream(t, stream)

	buf, err := readSOLFor(stream, 3*time.Second)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stream read: %v", err)
	}
	if len(buf) == 0 {
		t.Logf("no bytes in 3s (expected when PowerState=Off)")
		return
	}
	t.Logf("read n=%d preview=%q", len(buf), sanitizeConsole(buf, 200))
}

func TestLiveOpenSOL_ResetAndRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	bmc, sys := liveBMC(t, ctx)
	defer func() { _ = bmc.Close(context.Background()) }()
	stream := liveOpenSOL(t, ctx, bmc, sys.ID)

	var (
		mu   sync.Mutex
		got  bytes.Buffer
		rerr error
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := stream.Read(buf)
			if n > 0 {
				mu.Lock()
				if got.Len() < 256<<10 {
					_, _ = got.Write(buf[:n])
				}
				mu.Unlock()
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					rerr = err
				}
				return
			}
		}
	}()

	resetType := "ForceRestart"
	if !strings.EqualFold(sys.PowerState, "On") {
		resetType = "On"
	}
	t.Logf("Reset %s (was PowerState=%s)", resetType, sys.PowerState)
	if err := bmc.Reset(ctx, sys.ID, resetType); err != nil {
		t.Fatalf("Reset %s: %v", resetType, err)
	}

	deadline := time.Now().Add(90 * time.Second)
	var last int
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Second)
		mu.Lock()
		n := got.Len()
		mu.Unlock()
		if n != last {
			t.Logf("sol bytes so far: %d", n)
			last = n
		}
	}
	closeStream(t, stream)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Log("reader still blocked after Close")
	}
	mu.Lock()
	capture := got.Bytes()
	mu.Unlock()
	t.Logf("capture n=%d read_err=%v printable=\n%s", len(capture), rerr, sanitizeConsole(capture, 4000))
	if len(bytes.TrimSpace(stripANSI(capture))) < 8 {
		t.Fatalf("expected console text after %s, got %d bytes (%q)", resetType, len(capture), sanitizeConsole(capture, 120))
	}
}

func liveBMC(t *testing.T, ctx context.Context) (redfish.BMC, redfish.SystemInfo) {
	t.Helper()
	base := os.Getenv("SHOAL_BMC_URL")
	user := os.Getenv("SHOAL_BMC_USERNAME")
	pass := os.Getenv("SHOAL_BMC_PASSWORD")
	if base == "" || user == "" || pass == "" {
		t.Skip("SHOAL_BMC_URL / SHOAL_BMC_USERNAME / SHOAL_BMC_PASSWORD required")
	}
	tlsMode := os.Getenv("SHOAL_REDFISH_TLS_MODE")
	if tlsMode == "" {
		tlsMode = "insecure"
	}
	authMode := os.Getenv("SHOAL_REDFISH_AUTH_MODE")
	if authMode == "" {
		authMode = "basic"
	}
	bmc, err := redfish.NewBMC(redfish.Config{
		BaseURL:       base,
		Username:      user,
		Password:      pass,
		AuthMode:      authMode,
		TLSMode:       tlsMode,
		MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := bmc.Open(ctx); err != nil {
		t.Fatalf("open bmc: %v", err)
	}
	systems, err := bmc.ListSystems(ctx)
	if err != nil || len(systems) == 0 {
		t.Fatalf("systems: %v n=%d", err, len(systems))
	}
	sys := systems[0]
	t.Logf("system id=%s name=%s manufacturer=%s model=%s power=%s",
		sys.ID, sys.Name, sys.Manufacturer, sys.Model, sys.PowerState)
	return bmc, sys
}

func liveOpenSOL(t *testing.T, ctx context.Context, bmc redfish.BMC, systemID string) redfish.SOLStream {
	t.Helper()
	stream, err := bmc.OpenSOL(ctx, systemID)
	if err != nil {
		var unsupported *redfish.SOLUnsupportedError
		if errors.As(err, &unsupported) {
			t.Logf("OpenSOL unsupported vendor=%s connect_types=%v", unsupported.Vendor, unsupported.ConnectTypes)
			logDebug(t, unsupported.Debug)
		}
		t.Fatalf("OpenSOL: %v", err)
	}
	t.Logf("OpenSOL ok kind=%s vendor=%s", stream.Kind, stream.Vendor)
	logDebug(t, stream.Debug)
	return stream
}

func closeStream(t *testing.T, stream redfish.SOLStream) {
	t.Helper()
	if stream.ReadCloser == nil {
		return
	}
	if err := stream.Close(); err != nil {
		t.Logf("stream close: %v", err)
	}
}

func readSOLFor(r io.Reader, d time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	type nerr struct {
		buf []byte
		err error
	}
	ch := make(chan nerr, 1)
	go func() {
		buf := make([]byte, 4096)
		n, err := r.Read(buf)
		ch <- nerr{buf: buf[:n], err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, context.DeadlineExceeded
	case got := <-ch:
		if got.err != nil && got.err != io.EOF && len(got.buf) == 0 {
			return got.buf, got.err
		}
		return got.buf, nil
	}
}

func stripANSI(b []byte) []byte {
	var out []byte
	for i := 0; i < len(b); {
		if b[i] == 0x1b && i+1 < len(b) && b[i+1] == '[' {
			i += 2
			for i < len(b) && (b[i] < 0x40 || b[i] > 0x7e) {
				i++
			}
			if i < len(b) {
				i++
			}
			continue
		}
		out = append(out, b[i])
		i++
	}
	return out
}

func sanitizeConsole(b []byte, max int) string {
	var bld strings.Builder
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		i += size
		if r == utf8.RuneError && size == 1 {
			continue
		}
		if r == '\n' || r == '\r' || r == '\t' || (r >= 32 && r < 127) {
			bld.WriteRune(r)
		}
	}
	s := strings.TrimSpace(bld.String())
	lower := strings.ToLower(s)
	for _, bad := range []string{"password", "passwd", "secret", "token"} {
		if strings.Contains(lower, bad) {
			return "[redacted: body contained sensitive key]"
		}
	}
	if max > 0 && len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

func logDebug(t *testing.T, steps []redfish.CaptureDebugStep) {
	t.Helper()
	for i, s := range steps {
		t.Logf("  %d. [%s] ok=%v vendor=%s %s %s status=%d msg=%q elapsed=%dms",
			i+1, s.Phase, s.OK, s.Vendor, s.Method, s.URL, s.StatusCode, s.Message, s.ElapsedMS)
	}
}
