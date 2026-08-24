package sol

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/mattcburns/shoal/internal/common/redfish"
	"github.com/mattcburns/shoal/internal/common/secrets"
)

// RedfishTransport implements Transport over redfish.BMC.OpenSOL. It opens its
// own short-lived BMC session distinct from any BMC the orchestrator already
// holds open for virtual media/power — discovery happens entirely inside
// OpenSOL, so only the resulting WS/SSH stream is held for the life of the
// watch, minimizing pressure on real-BMC concurrent-session limits.
//
// Credentials are never resolved until Open(ctx, ...) so the lookup can be
// bound to the caller's context; WatchSession/RedfishTransport only ever
// carry the opaque CredentialRef, never a raw password (Golden Rule 3).
type RedfishTransport struct {
	NewBMC   redfish.Factory
	AuthMode string
	TLSMode  string
	CAFile   string
	SystemID string

	// Secrets resolves CredentialRef into a username/password at Open time.
	Secrets       secrets.Backend
	CredentialRef string

	mu     sync.Mutex
	bmc    redfish.BMC
	stream redfish.SOLStream
	cancel context.CancelFunc
}

// Open dials target (a Redfish BMC base URL), opens the SOL stream for
// SystemID via redfish.BMC.OpenSOL, and adapts the raw byte stream into a
// line channel using the same bufio.Scanner pattern as LibvirtTransport.Open.
func (t *RedfishTransport) Open(ctx context.Context, target string) (<-chan string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if target == "" {
		return nil, fmt.Errorf("sol: empty redfish BMC target")
	}
	if t.NewBMC == nil {
		return nil, fmt.Errorf("sol: redfish transport missing NewBMC factory")
	}

	var cred secrets.Credential
	if t.CredentialRef != "" {
		if t.Secrets == nil {
			return nil, fmt.Errorf("sol: redfish transport has credential_ref but no secrets backend")
		}
		var err error
		cred, err = t.Secrets.Get(ctx, t.CredentialRef)
		if err != nil {
			return nil, fmt.Errorf("sol: redfish transport: resolve credential_ref: %w", err)
		}
	}

	bmc, err := t.NewBMC(redfish.Config{
		BaseURL:  target,
		Username: cred.Username,
		Password: cred.Password,
		AuthMode: t.AuthMode,
		TLSMode:  t.TLSMode,
		CAFile:   t.CAFile,
	})
	if err != nil {
		return nil, fmt.Errorf("sol: redfish transport: new bmc: %w", err)
	}
	if err := bmc.Open(ctx); err != nil {
		return nil, fmt.Errorf("sol: redfish transport: open bmc: %w", err)
	}

	stream, err := bmc.OpenSOL(ctx, t.SystemID)
	if err != nil {
		_ = bmc.Close(context.Background())
		return nil, fmt.Errorf("sol: redfish transport: open sol: %w", err)
	}
	slog.Info("sol watch attached", "sol_kind", string(stream.Kind), "vendor", string(stream.Vendor))

	t.bmc = bmc
	t.stream = stream
	scanCtx, cancel := context.WithCancel(ctx)
	t.cancel = cancel

	ch := make(chan string, 32)
	go func() {
		defer close(ch)
		sc := bufio.NewScanner(stream)
		buf := make([]byte, 0, 64*1024)
		sc.Buffer(buf, 1024*1024)
		for sc.Scan() {
			select {
			case <-scanCtx.Done():
				return
			case ch <- sc.Text():
			}
		}
	}()
	return ch, nil
}

// Close cancels the scan loop and releases the SOL stream and BMC session.
// Both releases are best-effort so Close never blocks past what the
// underlying stream/BMC implementation itself takes (WatchService.Unregister
// bounds the caller-side wait separately).
func (t *RedfishTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
	var firstErr error
	if t.stream.ReadCloser != nil {
		if err := t.stream.Close(); err != nil {
			firstErr = err
		}
		t.stream = redfish.SOLStream{}
	}
	if t.bmc != nil {
		if err := t.bmc.Close(context.Background()); err != nil && firstErr == nil {
			firstErr = err
		}
		t.bmc = nil
	}
	return firstErr
}

var _ Transport = (*RedfishTransport)(nil)
