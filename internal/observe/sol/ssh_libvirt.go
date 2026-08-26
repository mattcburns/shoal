package sol

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
)

// SSHSerialConfig configures remote libvirt serial access (no runtime state).
type SSHSerialConfig struct {
	Host string // e.g. 192.168.122.100
	User string // e.g. lab
	// KeyPath is optional private key for ssh -i (BatchMode).
	KeyPath string
	// SSH binary (default "ssh").
	SSH string
	// RemoteVirsh is the virsh binary on the remote host (default "virsh").
	RemoteVirsh string
	// UseSudo prefixes remote commands with sudo -n.
	UseSudo bool
}

// SSHLibvirtTransport tails a nested libvirt domain serial console over SSH.
// Used for VM-hosted lab mode where domains run on L1 and Shoal runs on L0.
// Target is the libvirt domain name (e.g. shoal-node-1).
type SSHLibvirtTransport struct {
	cfg SSHSerialConfig

	mu       sync.Mutex
	cancel   context.CancelFunc
	cmd      *exec.Cmd
	stdout   io.ReadCloser
	waitOnce sync.Once
	waitErr  error
	activity chan struct{}
}

// NewSSHLibvirtTransport constructs a transport from config.
func NewSSHLibvirtTransport(cfg SSHSerialConfig) *SSHLibvirtTransport {
	return &SSHLibvirtTransport{cfg: cfg}
}

// Activity implements ActivityReporter.
func (t *SSHLibvirtTransport) Activity() <-chan struct{} { return t.activity }

// Open starts an SSH session that cats the domain console PTY.
func (t *SSHLibvirtTransport) Open(ctx context.Context, target string) (<-chan string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if target == "" {
		return nil, fmt.Errorf("sol: empty serial target")
	}
	if t.cfg.Host == "" {
		return nil, fmt.Errorf("sol: SSH serial host not configured")
	}
	user := t.cfg.User
	if user == "" {
		user = "lab"
	}
	sshBin := t.cfg.SSH
	if sshBin == "" {
		sshBin = "ssh"
	}
	virsh := t.cfg.RemoteVirsh
	if virsh == "" {
		virsh = "virsh"
	}

	prefix := ""
	if t.cfg.UseSudo {
		prefix = "sudo -n "
	}

	// Resolve tty then cat. stdbuf reduces line buffering when available.
	remote := fmt.Sprintf(
		`set -e; PTY=$(%s%s ttyconsole %s); test -n "$PTY"; if command -v stdbuf >/dev/null 2>&1; then %sstdbuf -oL -eL cat "$PTY"; else %scat "$PTY"; fi`,
		prefix, virsh, shellQuote(target), prefix, prefix,
	)

	args := []string{
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=10",
	}
	if t.cfg.KeyPath != "" {
		args = append(args, "-i", t.cfg.KeyPath)
	}
	args = append(args, user+"@"+t.cfg.Host, remote)

	// Detach process lifetime from the watch context: Open/Close own the process.
	// Tying ssh to watchCtx made cancel races with pipe drain hang cmd.Wait.
	cmd := exec.Command(sshBin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("sol: ssh stdout: %w", err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("sol: ssh start: %w", err)
	}
	t.cmd = cmd
	t.stdout = stdout
	t.waitOnce = sync.Once{}
	t.waitErr = nil

	scanCtx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel
	t.activity = make(chan struct{}, 8)
	dbg := solDebugFile("sshvirt", target)
	ch := make(chan string, 32)

	go func() {
		defer close(ch)
		defer closeIfSet(dbg)
		sc := bufio.NewScanner(&activityReader{r: stdout, activity: t.activity, tee: dbg})
		buf := make([]byte, 0, 64*1024)
		sc.Buffer(buf, 1024*1024)
		for sc.Scan() {
			select {
			case <-scanCtx.Done():
				return
			case ch <- sc.Text():
			}
		}
		// Natural EOF: reap process. Close may also reap via waitOnce.
		_ = t.reap()
	}()

	return ch, nil
}

func (t *SSHLibvirtTransport) reap() error {
	t.waitOnce.Do(func() {
		t.mu.Lock()
		cmd := t.cmd
		t.mu.Unlock()
		if cmd != nil {
			t.waitErr = cmd.Wait()
		}
	})
	return t.waitErr
}

// Close stops the SSH cat process. It never blocks indefinitely.
func (t *SSHLibvirtTransport) Close() error {
	t.mu.Lock()
	cancel := t.cancel
	t.cancel = nil
	cmd := t.cmd
	stdout := t.stdout
	t.stdout = nil
	t.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	// Unblock Scanner / ssh write side before Wait.
	if stdout != nil {
		_ = stdout.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}

	done := make(chan error, 1)
	go func() { done <- t.reap() }()
	select {
	case err := <-done:
		t.mu.Lock()
		t.cmd = nil
		t.mu.Unlock()
		return err
	case <-time.After(3 * time.Second):
		t.mu.Lock()
		t.cmd = nil
		t.mu.Unlock()
		return fmt.Errorf("sol: ssh wait timed out after kill")
	}
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// NewTransportFactory returns a WatchService-compatible factory.
// When cfg.Host is non-empty, domain-name targets use SSHLibvirtTransport.
// Absolute path targets always use local LibvirtTransport.
func NewTransportFactory(cfg SSHSerialConfig) func(session models.WatchSession) Transport {
	return func(session models.WatchSession) Transport {
		if strings.HasPrefix(session.Target, "/") {
			return &LibvirtTransport{}
		}
		if cfg.Host != "" {
			return NewSSHLibvirtTransport(cfg)
		}
		return &LibvirtTransport{}
	}
}
