package sol_test

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/observe/sol"
)

func TestRedfishTransportOpenReadsLines(t *testing.T) {
	fake := redfish.NewFake()
	fake.SOLStream = redfish.NewFakeSOLLines(redfish.SOLConnectSSH,
		"SHOAL|1|1|2026-07-19T00:00:00Z|BOOT|1|OK|hi",
		"SHOAL|1|2|2026-07-19T00:00:01Z|DONE|100|OK|bye",
	)

	tr := &sol.RedfishTransport{
		NewBMC:   func(redfish.Config) (redfish.BMC, error) { return fake, nil },
		SystemID: "1",
	}

	lines, err := tr.Open(context.Background(), "http://127.0.0.1:9999")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	var got []string
	deadline := time.After(2 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatal("channel closed early")
			}
			got = append(got, l)
		case <-deadline:
			t.Fatal("timed out waiting for lines")
		}
	}
	if got[0] != "SHOAL|1|1|2026-07-19T00:00:00Z|BOOT|1|OK|hi" {
		t.Fatalf("line 0 = %q", got[0])
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestRedfishTransportOpenSurfacesNewBMCError(t *testing.T) {
	tr := &sol.RedfishTransport{
		NewBMC:   func(redfish.Config) (redfish.BMC, error) { return nil, fmt.Errorf("boom") },
		SystemID: "1",
	}
	if _, err := tr.Open(context.Background(), "http://127.0.0.1:9999"); err == nil {
		t.Fatal("expected error from NewBMC failure")
	}
}

func TestRedfishTransportOpenSurfacesOpenSOLError(t *testing.T) {
	fake := redfish.NewFake()
	fake.SOLErr = fmt.Errorf("no sol path")
	tr := &sol.RedfishTransport{
		NewBMC:   func(redfish.Config) (redfish.BMC, error) { return fake, nil },
		SystemID: "1",
	}
	if _, err := tr.Open(context.Background(), "http://127.0.0.1:9999"); err == nil {
		t.Fatal("expected error from OpenSOL failure")
	}
}

func TestRedfishTransportOpenRequiresTarget(t *testing.T) {
	tr := &sol.RedfishTransport{
		NewBMC: func(redfish.Config) (redfish.BMC, error) { return redfish.NewFake(), nil },
	}
	if _, err := tr.Open(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty target")
	}
}

// TestRedfishTransportCloseUnblocksScan proves Close() releases a scan loop
// blocked on a read with no data available — the same contract
// LibvirtTransport relies on (ctx is only observed between successful scans,
// not while blocked inside one; closing the underlying stream is what
// unblocks a pending Read).
func TestRedfishTransportCloseUnblocksScan(t *testing.T) {
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })

	fake := redfish.NewFake()
	fake.SOLStream = redfish.SOLStream{ReadCloser: pr, Kind: redfish.SOLConnectSSH}

	tr := &sol.RedfishTransport{
		NewBMC:   func(redfish.Config) (redfish.BMC, error) { return fake, nil },
		SystemID: "1",
	}

	lines, err := tr.Open(context.Background(), "http://127.0.0.1:9999")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- tr.Close() }()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return promptly while a read was blocked")
	}

	select {
	case _, ok := <-lines:
		if ok {
			t.Fatal("expected channel to close after Close(), got a value")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for channel close after Close()")
	}
}
