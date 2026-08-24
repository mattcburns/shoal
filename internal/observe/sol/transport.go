package sol

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// Transport opens a line-oriented serial/console stream.
type Transport interface {
	// Open starts the stream and returns a channel of lines (without trailing newline).
	// The channel is closed when the stream ends or Close is called.
	Open(ctx context.Context, target string) (<-chan string, error)
	Close() error
}

// ActivityReporter is an optional capability a Transport may implement to
// report raw connection activity independent of whether a complete line has
// been scanned yet. WatchService.run type-asserts for it so its stall timer
// reflects "the console is alive" rather than just "the console printed a
// parseable SHOAL| marker" -- some console UIs (Dell's Lifecycle Controller /
// Unified Server Configurator BIOS-config screen, for one) repaint via
// cursor-positioning escape sequences with few or no newlines for minutes at
// a time, during which bufio.Scanner emits nothing on the lines channel even
// though the connection is very much alive. Confirmed live via a raw SOL
// capture during exactly that screen: a job reported "stall" while the BMC
// was still mid-boot, not actually stuck.
type ActivityReporter interface {
	// Activity returns a channel that receives a best-effort (non-blocking,
	// may drop under backpressure) ping on every raw byte read from the
	// underlying connection. Never closed by the transport; callers should
	// stop reading it once the watch's own context is done.
	Activity() <-chan struct{}
}

// activityReader wraps r and, on every successful Read, best-effort pings
// (non-blocking; drops under backpressure) a dedicated activity channel --
// kept separate from the lines channel so Transport.Open's documented
// contract ("a channel of lines") stays true for every caller. See
// ActivityReporter.
type activityReader struct {
	r        io.Reader
	activity chan<- struct{}
}

func (a *activityReader) Read(p []byte) (int, error) {
	n, err := a.r.Read(p)
	if n > 0 && a.activity != nil {
		select {
		case a.activity <- struct{}{}:
		default:
		}
	}
	return n, err
}

// errorTransport fails Open with a fixed error. Used for unrecognized
// session.Transport values so unknown/unwired transports fail loudly instead
// of silently running as libvirt (or anything else).
type errorTransport struct{ err error }

func (e *errorTransport) Open(context.Context, string) (<-chan string, error) { return nil, e.err }
func (e *errorTransport) Close() error                                        { return nil }

// ReaderTransport reads lines from an io.Reader (tests / injected pipes).
type ReaderTransport struct {
	mu       sync.Mutex
	cancel   context.CancelFunc
	r        io.ReadCloser
	activity chan struct{}
}

// NewReaderTransport wraps r as a Transport. Target is ignored on Open.
func NewReaderTransport(r io.ReadCloser) *ReaderTransport {
	return &ReaderTransport{r: r}
}

// Activity implements ActivityReporter.
func (t *ReaderTransport) Activity() <-chan struct{} { return t.activity }

// Open starts reading lines from the reader.
func (t *ReaderTransport) Open(ctx context.Context, _ string) (<-chan string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.r == nil {
		return nil, fmt.Errorf("sol: nil reader")
	}
	ctx, cancel := context.WithCancel(ctx)
	t.cancel = cancel
	r := t.r
	t.activity = make(chan struct{}, 8)
	ch := make(chan string, 32)
	go func() {
		defer close(ch)
		sc := bufio.NewScanner(&activityReader{r: r, activity: t.activity})
		// large lines possible on serial
		buf := make([]byte, 0, 64*1024)
		sc.Buffer(buf, 1024*1024)
		for sc.Scan() {
			select {
			case <-ctx.Done():
				return
			case ch <- sc.Text():
			}
		}
	}()
	return ch, nil
}

// Close cancels the reader loop and closes the underlying reader.
func (t *ReaderTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
	if t.r != nil {
		err := t.r.Close()
		// keep r set until after Close so a racing Open capture is safe;
		// callers must not Open again after Close without a new transport.
		t.r = nil
		return err
	}
	return nil
}

// LibvirtTransport resolves a domain console via `virsh ttyconsole` and tails the PTY.
// Target is the libvirt domain name (or an absolute path to a PTY/device).
type LibvirtTransport struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	file   *os.File
	// Virsh is the virsh binary (default "virsh").
	Virsh    string
	activity chan struct{}
}

// Activity implements ActivityReporter.
func (t *LibvirtTransport) Activity() <-chan struct{} { return t.activity }

// Open attaches to the domain serial console.
func (t *LibvirtTransport) Open(ctx context.Context, target string) (<-chan string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if target == "" {
		return nil, fmt.Errorf("sol: empty serial target")
	}

	path := target
	if !strings.HasPrefix(target, "/") {
		virsh := t.Virsh
		if virsh == "" {
			virsh = "virsh"
		}
		out, err := exec.CommandContext(ctx, virsh, "ttyconsole", target).Output()
		if err != nil {
			return nil, fmt.Errorf("sol: virsh ttyconsole %s: %w", target, err)
		}
		path = strings.TrimSpace(string(out))
		if path == "" {
			return nil, fmt.Errorf("sol: empty ttyconsole for %s", target)
		}
	}

	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("sol: open console %s: %w", path, err)
	}
	t.file = f
	ctx, cancel := context.WithCancel(ctx)
	t.cancel = cancel
	t.activity = make(chan struct{}, 8)
	ch := make(chan string, 32)
	go func() {
		defer close(ch)
		defer func() {
			// Scanner/PTY edge cases must not kill the process.
			_ = recover()
		}()
		sc := bufio.NewScanner(&activityReader{r: f, activity: t.activity}) // local f, not t.file
		buf := make([]byte, 0, 64*1024)
		sc.Buffer(buf, 1024*1024)
		for sc.Scan() {
			select {
			case <-ctx.Done():
				return
			case ch <- sc.Text():
			}
		}
	}()
	return ch, nil
}

// Close stops the tail and closes the PTY handle.
func (t *LibvirtTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
	if t.file != nil {
		err := t.file.Close()
		t.file = nil
		return err
	}
	return nil
}
