package iso

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Install modes for the live / install image.
const (
	InstallModeSimulate    = "simulate"    // Phase 2 marker demo (default)
	InstallModeWrite       = "write"       // Phase 6a: write /payload with real SOL progress
	InstallModeAutoinstall = "autoinstall" // Phase 7a: Ubuntu autoinstall remaster
)

// BuildInput describes a live-image build request.
type BuildInput struct {
	// Name is the output basename without path (default shoal-marker.iso).
	Name string
	// OutDir is the directory for the local artifact (default os.TempDir or CWD).
	OutDir string
	// EmbeddedPayload is non-secret text written into the image as /payload.
	// Secrets must never be passed here. Prefer PayloadFile for binary images.
	EmbeddedPayload string
	// PayloadFile is a host path copied into the image as /payload (binary-safe).
	PayloadFile string
	// InstallMode is simulate (default), write (Phase 6a), or autoinstall (Phase 7a).
	InstallMode string
	// InstallTarget is optional block device or file path baked into the image
	// (overridable via kernel cmdline shoal.target=). Write mode only.
	InstallTarget string
	// UbuntuBaseISO is the official Ubuntu Server live-server ISO path (legacy remaster path).
	// May also be supplied via SHOAL_UBUNTU_ISO in the environment.
	UbuntuBaseISO string
	// UbuntuCloudImg is an Ubuntu cloud image (.img) used for the reliable
	// Phase 7a nested-lab path: prepare raw payload + marker write ISO.
	// May also be supplied via SHOAL_UBUNTU_CLOUD_IMG.
	UbuntuCloudImg string
	// Hostname for autoinstall identity (default shoal-node).
	Hostname string
	// Username for autoinstall identity (default ubuntu for cloud images).
	// Lab-only password via SHOAL_AUTOINSTALL_PASSWORD (not logged by Go).
	Username string
	// ScriptPath overrides the build script (empty = auto-discover by mode).
	ScriptPath string
}

// Artifact is a local ISO file produced by Build.
type Artifact struct {
	Path string
	Name string
	Size int64
}

// PublishDest is the lab ISO HTTP tree (or a local stand-in).
type PublishDest struct {
	// Dir is the filesystem directory served by nginx (e.g. /srv/iso).
	Dir string
	// BaseURL is the public prefix (e.g. http://192.168.124.1:8080) without trailing slash.
	BaseURL string
}

// Builder builds and publishes bootable live ISOs for Virtual Media.
type Builder interface {
	Build(ctx context.Context, in BuildInput) (Artifact, error)
	Publish(ctx context.Context, art Artifact, dest PublishDest) (string, error)
}

// ScriptBuilder wraps infra/scripts/build-marker-iso.sh via os/exec.
type ScriptBuilder struct {
	// ScriptPath is the absolute or relative path to build-marker-iso.sh.
	// Empty uses FindBuildScript.
	ScriptPath string
	Log        *slog.Logger
}

// NewScriptBuilder constructs a ScriptBuilder.
func NewScriptBuilder(scriptPath string, log *slog.Logger) *ScriptBuilder {
	if log == nil {
		log = slog.Default()
	}
	return &ScriptBuilder{ScriptPath: scriptPath, Log: log}
}

// Build runs the marker or Ubuntu autoinstall ISO script (host tools required).
func (b *ScriptBuilder) Build(ctx context.Context, in BuildInput) (Artifact, error) {
	mode := strings.TrimSpace(in.InstallMode)
	if mode == "" {
		mode = InstallModeSimulate
	}
	if mode != InstallModeSimulate && mode != InstallModeWrite && mode != InstallModeAutoinstall {
		return Artifact{}, fmt.Errorf("iso: invalid install mode %q (want simulate|write|autoinstall)", mode)
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		if mode == InstallModeAutoinstall {
			name = "shoal-ubuntu-autoinstall.iso"
		} else {
			name = "shoal-marker.iso"
		}
	}
	if !strings.HasSuffix(strings.ToLower(name), ".iso") {
		name += ".iso"
	}
	// Sanitize basename only.
	name = filepath.Base(name)
	outDir := in.OutDir
	if outDir == "" {
		outDir = os.TempDir()
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return Artifact{}, fmt.Errorf("iso: mkdir out: %w", err)
	}
	outPath := filepath.Join(outDir, name)

	// Autoinstall builds (prepare + pack) can be slow.
	if mode == InstallModeAutoinstall {
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, 45*time.Minute)
			defer cancel()
		}
	}

	// Phase 7a preferred path: Ubuntu cloud image → customized raw.gz → marker write ISO.
	// Nested sushy/libvirt boots our marker ISO reliably; live-server remaster often does not.
	cloudImg := strings.TrimSpace(in.UbuntuCloudImg)
	if cloudImg == "" {
		cloudImg = strings.TrimSpace(os.Getenv("SHOAL_UBUNTU_CLOUD_IMG"))
	}
	payloadFile := strings.TrimSpace(in.PayloadFile)
	if mode == InstallModeAutoinstall && payloadFile == "" && cloudImg != "" {
		prep, err := FindCloudPrepareScript()
		if err != nil {
			return Artifact{}, err
		}
		rawOut := filepath.Join(outDir, "ubuntu-cloud-payload.raw")
		prepCmd := exec.CommandContext(ctx, prep, rawOut)
		prepCmd.Env = append(os.Environ(),
			"SHOAL_UBUNTU_CLOUD_IMG="+cloudImg,
			"SHOAL_CLOUD_PAYLOAD_GZIP=1",
		)
		if h := strings.TrimSpace(in.Hostname); h != "" {
			prepCmd.Env = append(prepCmd.Env, "SHOAL_AUTOINSTALL_HOSTNAME="+h)
		}
		if u := strings.TrimSpace(in.Username); u != "" {
			prepCmd.Env = append(prepCmd.Env, "SHOAL_AUTOINSTALL_USERNAME="+u)
		}
		b.Log.Info("preparing ubuntu cloud payload", "img", cloudImg, "out", rawOut)
		if out, err := prepCmd.CombinedOutput(); err != nil {
			return Artifact{}, fmt.Errorf("iso: prepare cloud payload: %w\n%s", err, truncate(string(out), 2<<10))
		}
		gz := rawOut + ".gz"
		if st, err := os.Stat(gz); err == nil && !st.IsDir() {
			payloadFile = gz
		} else {
			payloadFile = rawOut
		}
	}

	script := in.ScriptPath
	if script == "" {
		script = b.ScriptPath
	}
	useLiveRemaster := mode == InstallModeAutoinstall && payloadFile == "" &&
		(strings.TrimSpace(in.UbuntuBaseISO) != "" || strings.TrimSpace(os.Getenv("SHOAL_UBUNTU_ISO")) != "")
	if script == "" {
		var err error
		if useLiveRemaster && payloadFile == "" {
			script, err = FindUbuntuAutoinstallScript()
		} else {
			// autoinstall with cloud payload, write, simulate → marker ISO builder
			script, err = FindBuildScript()
		}
		if err != nil {
			return Artifact{}, err
		}
	}

	cmd := exec.CommandContext(ctx, script, outPath)
	cmd.Env = append(os.Environ(),
		"SHOAL_ISO_NAME="+name,
		"SHOAL_INSTALL_MODE="+mode,
	)

	if useLiveRemaster && payloadFile == "" {
		base := strings.TrimSpace(in.UbuntuBaseISO)
		if base == "" {
			base = strings.TrimSpace(os.Getenv("SHOAL_UBUNTU_ISO"))
		}
		if base == "" {
			return Artifact{}, fmt.Errorf("iso: autoinstall requires SHOAL_UBUNTU_CLOUD_IMG (preferred) or SHOAL_UBUNTU_ISO")
		}
		if st, err := os.Stat(base); err != nil || st.IsDir() {
			return Artifact{}, fmt.Errorf("iso: ubuntu base ISO not readable: %s", base)
		}
		cmd.Env = append(cmd.Env, "SHOAL_UBUNTU_ISO="+base)
		if h := strings.TrimSpace(in.Hostname); h != "" {
			cmd.Env = append(cmd.Env, "SHOAL_AUTOINSTALL_HOSTNAME="+h)
		}
		if u := strings.TrimSpace(in.Username); u != "" {
			cmd.Env = append(cmd.Env, "SHOAL_AUTOINSTALL_USERNAME="+u)
		}
	} else {
		if mode == InstallModeAutoinstall && payloadFile == "" {
			return Artifact{}, fmt.Errorf("iso: autoinstall requires SHOAL_UBUNTU_CLOUD_IMG (preferred nested-lab path) or payload-file or SHOAL_UBUNTU_ISO")
		}
		// Payload: prefer file path (binary-safe); else inline text via env.
		// Callers must not pass passwords.
		if payloadFile != "" {
			st, err := os.Stat(payloadFile)
			if err != nil {
				return Artifact{}, fmt.Errorf("iso: payload file: %w", err)
			}
			if st.IsDir() {
				return Artifact{}, fmt.Errorf("iso: payload file is a directory")
			}
			cmd.Env = append(cmd.Env, "SHOAL_PAYLOAD_FILE="+payloadFile)
		} else if p := strings.TrimSpace(in.EmbeddedPayload); p != "" {
			if looksSecretPayload(p) {
				return Artifact{}, fmt.Errorf("iso: embedded_payload must not contain secret-like content")
			}
			cmd.Env = append(cmd.Env, "SHOAL_EMBEDDED_PAYLOAD="+p)
		}
		if t := strings.TrimSpace(in.InstallTarget); t != "" {
			cmd.Env = append(cmd.Env, "SHOAL_INSTALL_TARGET="+t)
		} else if mode == InstallModeAutoinstall {
			cmd.Env = append(cmd.Env, "SHOAL_INSTALL_TARGET=/dev/vda")
		}
		if mode == InstallModeAutoinstall {
			cmd.Env = append(cmd.Env, "SHOAL_INSTALL_REBOOT=1")
		}
	}

	start := time.Now()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Artifact{}, fmt.Errorf("iso: build script failed: %w\n%s", err, truncate(string(out), 2<<10))
	}
	st, err := os.Stat(outPath)
	if err != nil {
		return Artifact{}, fmt.Errorf("iso: output missing after build: %w\n%s", err, truncate(string(out), 512))
	}
	b.Log.Info("iso built",
		"path", outPath,
		"size", st.Size(),
		"mode", mode,
		"elapsed_ms", time.Since(start).Milliseconds(),
	)
	return Artifact{Path: outPath, Name: name, Size: st.Size()}, nil
}

// BuildAndPublish builds then publishes; returns the public BMC-reachable URL.
func (b *ScriptBuilder) BuildAndPublish(ctx context.Context, in BuildInput, dest PublishDest) (string, Artifact, error) {
	art, err := b.Build(ctx, in)
	if err != nil {
		return "", Artifact{}, err
	}
	url, err := b.Publish(ctx, art, dest)
	if err != nil {
		return "", art, err
	}
	return url, art, nil
}

// Publish copies the artifact into dest.Dir and returns BaseURL/name.
func (b *ScriptBuilder) Publish(_ context.Context, art Artifact, dest PublishDest) (string, error) {
	if strings.TrimSpace(dest.Dir) == "" {
		return "", fmt.Errorf("iso: publish dir is empty (set SHOAL_ISO_PUBLISH_DIR)")
	}
	if strings.TrimSpace(dest.BaseURL) == "" {
		return "", fmt.Errorf("iso: base URL is empty (set SHOAL_ISO_BASE_URL)")
	}
	if art.Path == "" {
		return "", fmt.Errorf("iso: empty artifact path")
	}
	name := art.Name
	if name == "" {
		name = filepath.Base(art.Path)
	}
	name = filepath.Base(name)
	if err := os.MkdirAll(dest.Dir, 0o755); err != nil {
		return "", fmt.Errorf("iso: mkdir publish: %w", err)
	}
	destPath := filepath.Join(dest.Dir, name)
	if err := copyFile(art.Path, destPath); err != nil {
		return "", err
	}
	url, err := PublicURL(dest.BaseURL, name)
	if err != nil {
		return "", err
	}
	b.Log.Info("iso published", "path", destPath, "url", url)
	return url, nil
}

// PublicURL joins base URL and filename.
func PublicURL(baseURL, filename string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	name := filepath.Base(strings.TrimSpace(filename))
	if base == "" {
		return "", fmt.Errorf("iso: empty base URL")
	}
	if name == "" || name == "." || name == "/" {
		return "", fmt.Errorf("iso: empty filename")
	}
	return base + "/" + name, nil
}

// ResolveFromProfile turns profile.iso_base into a BMC-reachable URL.
//
// Rules:
//   - empty iso_base → error
//   - http(s)://… → returned as-is
//   - otherwise requires baseURL and appends .iso if missing
func ResolveFromProfile(isoBase, baseURL string) (string, error) {
	isoBase = strings.TrimSpace(isoBase)
	if isoBase == "" {
		return "", fmt.Errorf("iso: profile iso_base is empty")
	}
	lower := strings.ToLower(isoBase)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return isoBase, nil
	}
	name := isoBase
	if !strings.HasSuffix(lower, ".iso") {
		name += ".iso"
	}
	return PublicURL(baseURL, name)
}

// FindBuildScript locates infra/scripts/build-marker-iso.sh relative to cwd or common roots.
func FindBuildScript() (string, error) {
	if p := os.Getenv("SHOAL_ISO_BUILD_SCRIPT"); p != "" {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
		return "", fmt.Errorf("iso: SHOAL_ISO_BUILD_SCRIPT not found: %s", p)
	}
	return findScript("build-marker-iso.sh", "SHOAL_ISO_BUILD_SCRIPT")
}

// FindUbuntuAutoinstallScript locates infra/scripts/build-ubuntu-autoinstall-iso.sh.
func FindUbuntuAutoinstallScript() (string, error) {
	if p := os.Getenv("SHOAL_UBUNTU_AUTOINSTALL_SCRIPT"); p != "" {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
		return "", fmt.Errorf("iso: SHOAL_UBUNTU_AUTOINSTALL_SCRIPT not found: %s", p)
	}
	return findScript("build-ubuntu-autoinstall-iso.sh", "SHOAL_UBUNTU_AUTOINSTALL_SCRIPT")
}

// FindCloudPrepareScript locates infra/scripts/prepare-ubuntu-cloud-payload.sh.
func FindCloudPrepareScript() (string, error) {
	if p := os.Getenv("SHOAL_CLOUD_PREPARE_SCRIPT"); p != "" {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
		return "", fmt.Errorf("iso: SHOAL_CLOUD_PREPARE_SCRIPT not found: %s", p)
	}
	return findScript("prepare-ubuntu-cloud-payload.sh", "SHOAL_CLOUD_PREPARE_SCRIPT")
}

func findScript(basename, envHint string) (string, error) {
	candidates := []string{
		filepath.Join("infra/scripts", basename),
		filepath.Join("./infra/scripts", basename),
		filepath.Join("../infra/scripts", basename),
		filepath.Join("../../infra/scripts", basename),
	}
	wd, _ := os.Getwd()
	for i := 0; i < 6 && wd != ""; i++ {
		candidates = append(candidates, filepath.Join(wd, "infra/scripts", basename))
		parent := filepath.Dir(wd)
		if parent == wd {
			break
		}
		wd = parent
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			abs, err := filepath.Abs(c)
			if err != nil {
				return c, nil
			}
			return abs, nil
		}
	}
	return "", fmt.Errorf("iso: %s not found (set %s)", basename, envHint)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("iso: open source: %w", err)
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("iso: create dest: %w", err)
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("iso: copy: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("iso: rename: %w", err)
	}
	return nil
}

func looksSecretPayload(s string) bool {
	lower := strings.ToLower(s)
	for _, needle := range []string{"password", "passwd", "secret", "api_key", "apikey", "token=", "bearer "} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

var _ Builder = (*ScriptBuilder)(nil)
