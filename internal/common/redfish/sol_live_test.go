//go:build live_sol

package redfish_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strconv"
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

func TestLiveVirtualMediaISO(t *testing.T) {
	isoURL := os.Getenv("SHOAL_ISO_URL")
	if isoURL == "" {
		t.Skip("SHOAL_ISO_URL required (BMC-reachable HTTP ISO)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	bmc, sys := liveBMC(t, ctx)
	defer func() { _ = bmc.Close(context.Background()) }()

	vms, err := bmc.ListVirtualMedia(ctx, sys.ID)
	if err != nil {
		t.Fatalf("ListVirtualMedia: %v", err)
	}
	var slot redfish.VirtualMedia
	for _, vm := range vms {
		t.Logf("vm id=%s name=%s inserted=%v image=%s cd=%v types=%v uri=%s",
			vm.ID, vm.Name, vm.Inserted, vm.Image, vm.SupportsCD, vm.MediaTypes, vm.URI)
		if slot.URI == "" && vm.SupportsCD {
			slot = vm
		}
	}
	if slot.URI == "" && len(vms) > 0 {
		slot = vms[0]
	}
	if slot.URI == "" {
		t.Fatal("no virtual media slot")
	}
	t.Logf("insert %s into %s", isoURL, slot.URI)
	if err := bmc.InsertVirtualMedia(ctx, slot.URI, isoURL); err != nil {
		t.Fatalf("InsertVirtualMedia: %v", err)
	}
	defer func() {
		if err := bmc.EjectVirtualMedia(context.Background(), slot.URI); err != nil {
			t.Logf("eject: %v", err)
		}
	}()
	vms, err = bmc.ListVirtualMedia(ctx, sys.ID)
	if err != nil {
		t.Fatalf("list after insert: %v", err)
	}
	ok := false
	for _, vm := range vms {
		if vm.URI == slot.URI {
			t.Logf("after insert inserted=%v image=%s", vm.Inserted, vm.Image)
			ok = vm.Inserted && strings.Contains(vm.Image, "shoal-marker.iso")
		}
	}
	if !ok {
		t.Fatal("expected inserted shoal-marker.iso")
	}
}

// TestLiveMarkerBootCapture is a raw-SOL diagnostic: insert the marker ISO,
// set the one-time CD boot override, ForceRestart, and dump everything the
// console prints for several minutes -- unfiltered by the SHOAL| marker
// parser. Use this when a job stalls with last_marker_seq=0 to see directly
// whether the box is booting the CD and going silent, looping, or falling
// through to disk/PXE.
//
//	set -a && . ./.env && set +a
//	SHOAL_BMC_URL=https://172.16.21.202 \
//	SHOAL_ISO_URL=http://172.16.20.138:8080/shoal-marker.iso \
//	  go test ./internal/common/redfish -tags=live_sol -run TestLiveMarkerBootCapture -v -count=1 -timeout 12m
func TestLiveMarkerBootCapture(t *testing.T) {
	isoURL := os.Getenv("SHOAL_ISO_URL")
	if isoURL == "" {
		t.Skip("SHOAL_ISO_URL required (BMC-reachable HTTP ISO)")
	}
	ctxMinutes := 11
	if v := os.Getenv("SHOAL_CAPTURE_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			ctxMinutes = n + 2
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(ctxMinutes)*time.Minute)
	defer cancel()
	bmc, sys := liveBMC(t, ctx)
	defer func() { _ = bmc.Close(context.Background()) }()

	vms, err := bmc.ListVirtualMedia(ctx, sys.ID)
	if err != nil {
		t.Fatalf("ListVirtualMedia: %v", err)
	}
	inserted := 0
	for _, vm := range vms {
		t.Logf("vm id=%s name=%s inserted=%v image=%s cd=%v types=%v uri=%s",
			vm.ID, vm.Name, vm.Inserted, vm.Image, vm.SupportsCD, vm.MediaTypes, vm.URI)
		if !vm.SupportsCD {
			continue
		}
		t.Logf("insert %s into %s", isoURL, vm.URI)
		if err := bmc.InsertVirtualMedia(ctx, vm.URI, isoURL); err != nil {
			t.Logf("insert %s: %v (continuing)", vm.URI, err)
			continue
		}
		inserted++
	}
	if inserted == 0 {
		t.Fatal("no virtual media slot accepted the ISO")
	}

	if err := bmc.SetBootOverrideOnceCD(ctx, sys.ID); err != nil {
		t.Fatalf("SetBootOverrideOnceCD: %v", err)
	}
	boot, err := bmc.GetBoot(ctx, sys.ID)
	if err != nil {
		t.Fatalf("GetBoot: %v", err)
	}
	t.Logf("boot override after set: enabled=%s target=%s", boot.OverrideEnabled, boot.OverrideTarget)

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
				if got.Len() < 1<<20 {
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

	// Mirror the orchestrator's power path: ForceRestart, falling back to On
	// for a host that is powered off. The cold Power-On path matters: on this
	// R750 every observed success started warm (ForceRestart from On) and the
	// hard failures started cold, where POST does memory training / LC init
	// with long console-silent stretches.
	resetType := "ForceRestart"
	if err := bmc.Reset(ctx, sys.ID, resetType); err != nil {
		resetType = "On"
		if err2 := bmc.Reset(ctx, sys.ID, resetType); err2 != nil {
			t.Fatalf("Reset ForceRestart: %v (On fallback: %v)", err, err2)
		}
	}
	t.Logf("power action %s sent (was PowerState=%s)", resetType, sys.PowerState)

	captureMinutes := 9
	if v := os.Getenv("SHOAL_CAPTURE_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			captureMinutes = n
		}
	}
	deadline := time.Now().Add(time.Duration(captureMinutes) * time.Minute)
	var last int
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Second)
		mu.Lock()
		n := got.Len()
		snippet := sanitizeConsole(got.Bytes(), 4000)
		mu.Unlock()
		if n != last {
			t.Logf("sol bytes so far: %d\n--- tail ---\n%s\n--- end tail ---", n, tailLines(snippet, 20))
			last = n
		}
		if strings.Contains(snippet, "SHOAL|") {
			t.Log("SHOAL| marker observed -- boot succeeded, stopping capture early")
			break
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
	t.Logf("FULL CAPTURE n=%d read_err=%v\n%s", len(capture), rerr, sanitizeConsole(capture, 1<<20))
}

func tailLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
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
