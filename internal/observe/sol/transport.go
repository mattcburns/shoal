package sol

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
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
//
// When tee is non-nil every raw byte is also copied there (best-effort,
// write errors ignored) -- see solDebugFile.
type activityReader struct {
	r        io.Reader
	activity chan<- struct{}
	tee      io.Writer
}

func (a *activityReader) Read(p []byte) (int, error) {
	n, err := a.r.Read(p)
	if n > 0 {
		if a.tee != nil {
			_, _ = a.tee.Write(p[:n])
		}
		if a.activity != nil {
			select {
			case a.activity <- struct{}{}:
			default:
			}
		}
	}
	return n, err
}

// solDebugFile opens a raw-byte capture file for one SOL session when
// SHOAL_SOL_DEBUG_DIR is set (empty = disabled, the default). Returns nil on
// any failure -- debug capture must never break a watch. The file records the
// console exactly as the transport read it, unfiltered by line-splitting or
// marker parsing; this is the only way to see what a job's console actually
// did during a "phase never left WAITING_SOL" failure, because the job holds
// the (sometimes only) SOL session itself so an operator cannot attach a
// second capture alongside it. Raw boot console only -- no BMC credentials
// ever transit this stream.
func solDebugFile(kind, target string) io.WriteCloser {
	dir := strings.TrimSpace(os.Getenv("SHOAL_SOL_DEBUG_DIR"))
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	san := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-':
			return r
		default:
			return '_'
		}
	}, target)
	if len(san) > 64 {
		san = san[:64]
	}
	name := fmt.Sprintf("sol-%s-%s-%s.raw", kind, san, time.Now().UTC().Format("20060102T150405Z"))
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return nil
	}
	return f
}

// closeIfSet closes an optional debug tee (nil-safe helper for scan goroutines).
func closeIfSet(c io.Closer) {
	if c != nil {
		_ = c.Close()
	}
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
	dbg := solDebugFile("libvirt", target)
	ch := make(chan string, 32)
	go func() {
		defer close(ch)
		defer closeIfSet(dbg)
		defer func() {
			// Scanner/PTY edge cases must not kill the process.
			_ = recover()
		}()
		sc := bufio.NewScanner(&activityReader{r: f, activity: t.activity, tee: dbg}) // local f, not t.file
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
