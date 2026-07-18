package iso_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattcburns/shoal/internal/deploy/iso"
)

func TestPublicURL(t *testing.T) {
	u, err := iso.PublicURL("http://192.168.124.1:8080/", "shoal-marker.iso")
	if err != nil {
		t.Fatal(err)
	}
	if u != "http://192.168.124.1:8080/shoal-marker.iso" {
		t.Fatalf("got %q", u)
	}
}

func TestResolveFromProfile(t *testing.T) {
	u, err := iso.ResolveFromProfile("http://iso/x.iso", "")
	if err != nil || u != "http://iso/x.iso" {
		t.Fatalf("%q %v", u, err)
	}
	u, err = iso.ResolveFromProfile("ubuntu-22.04-live", "http://192.168.124.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	if u != "http://192.168.124.1:8080/ubuntu-22.04-live.iso" {
		t.Fatalf("got %q", u)
	}
	if _, err := iso.ResolveFromProfile("name-only", ""); err == nil {
		t.Fatal("expected error without base URL")
	}
}

func TestPublishCopiesFile(t *testing.T) {
	srcDir := t.TempDir()
	pubDir := t.TempDir()
	src := filepath.Join(srcDir, "test.iso")
	if err := os.WriteFile(src, []byte("ISO-BYTES"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := iso.NewScriptBuilder("", slog.New(slog.NewTextHandler(io.Discard, nil)))
	url, err := b.Publish(context.Background(), iso.Artifact{Path: src, Name: "test.iso"}, iso.PublishDest{
		Dir: pubDir, BaseURL: "http://lab:8080",
	})
	if err != nil {
		t.Fatal(err)
	}
	if url != "http://lab:8080/test.iso" {
		t.Fatalf("url %q", url)
	}
	got, err := os.ReadFile(filepath.Join(pubDir, "test.iso"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ISO-BYTES" {
		t.Fatalf("content %q", got)
	}
}

func TestBuildRejectsSecretPayload(t *testing.T) {
	b := iso.NewScriptBuilder("/nonexistent-script", slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err := b.Build(context.Background(), iso.BuildInput{
		EmbeddedPayload: "password=hunter2",
		OutDir:          t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "secret") {
		t.Fatalf("expected secret rejection, got %v", err)
	}
}

func TestBuildRejectsBadInstallMode(t *testing.T) {
	b := iso.NewScriptBuilder("/nonexistent-script", slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err := b.Build(context.Background(), iso.BuildInput{
		InstallMode: "explode",
		OutDir:      t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "install mode") {
		t.Fatalf("expected mode error, got %v", err)
	}
}

func TestBuildAutoinstallRequiresCloudOrISO(t *testing.T) {
	b := iso.NewScriptBuilder("/nonexistent-script", slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Setenv("SHOAL_UBUNTU_ISO", "")
	t.Setenv("SHOAL_UBUNTU_CLOUD_IMG", "")
	_, err := b.Build(context.Background(), iso.BuildInput{
		InstallMode: iso.InstallModeAutoinstall,
		OutDir:      t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected autoinstall input error")
	}
	if !strings.Contains(err.Error(), "CLOUD") && !strings.Contains(err.Error(), "payload") && !strings.Contains(err.Error(), "UBUNTU") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFindUbuntuAutoinstallScript(t *testing.T) {
	p, err := iso.FindUbuntuAutoinstallScript()
	if err != nil {
		t.Skipf("script not found in this environment: %v", err)
	}
	if !strings.Contains(p, "build-ubuntu-autoinstall-iso.sh") {
		t.Fatalf("unexpected path %q", p)
	}
}

func TestAutoinstallTemplateExists(t *testing.T) {
	// Template must ship with the repo for Phase 7a.
	p, err := iso.FindUbuntuAutoinstallScript()
	if err != nil {
		t.Skip(err)
	}
	tmpl := filepath.Join(filepath.Dir(p), "autoinstall", "ubuntu-user-data.yaml.tmpl")
	b, err := os.ReadFile(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"autoinstall:", "{{HOSTNAME}}", "SHOAL|", "DONE"} {
		if !strings.Contains(s, want) {
			t.Fatalf("template missing %q", want)
		}
	}
}

func TestBuildRejectsMissingPayloadFile(t *testing.T) {
	b := iso.NewScriptBuilder("/nonexistent-script", slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err := b.Build(context.Background(), iso.BuildInput{
		PayloadFile: filepath.Join(t.TempDir(), "missing.bin"),
		OutDir:      t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected payload file error")
	}
}

func TestFindBuildScript(t *testing.T) {
	// From module root this should resolve in normal checkouts.
	p, err := iso.FindBuildScript()
	if err != nil {
		t.Skipf("script not found in this environment: %v", err)
	}
	if !strings.Contains(p, "build-marker-iso.sh") {
		t.Fatalf("unexpected path %q", p)
	}
}
