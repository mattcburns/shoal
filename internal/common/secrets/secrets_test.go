package secrets_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/secrets"
)

func TestMemoryRoundTrip(t *testing.T) {
	ctx := context.Background()
	b := secrets.NewMemory()
	ref := "lab-node-1"
	if err := b.Put(ctx, ref, secrets.Credential{Username: "root", Password: "hunter2"}); err != nil {
		t.Fatal(err)
	}
	got, err := b.Get(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != "root" || got.Password != "hunter2" {
		t.Fatalf("got %+v", got)
	}
	if err := b.Delete(ctx, ref); err != nil {
		t.Fatal(err)
	}
	_, err = b.Get(ctx, ref)
	if !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestFileBackendMode0600(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	b, err := secrets.NewFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	ref := "cred-abc"
	if err := b.Put(ctx, ref, secrets.Credential{Username: "u", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	got, err := b.Get(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "p" {
		t.Fatalf("got %+v", got)
	}
	info, err := os.Stat(filepath.Join(dir, ref+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("file mode %o want 0600", perm)
	}
}

func TestNormalizedAssetNeverHoldsPassword(t *testing.T) {
	ctx := context.Background()
	b := secrets.NewMemory()
	ref := "device-serial-1"
	pass := "never-in-asset"
	if err := b.Put(ctx, ref, secrets.Credential{Username: "admin", Password: pass}); err != nil {
		t.Fatal(err)
	}
	asset := models.NormalizedAsset{
		Serial:        "SN1",
		Model:         "X",
		Vendor:        "Y",
		BMCIP:         "10.0.0.1",
		CredentialRef: ref,
	}
	raw, err := json.Marshal(asset)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), pass) {
		t.Fatalf("password leaked into NormalizedAsset JSON: %s", raw)
	}
	// Resolve via ref
	cred, err := b.Get(ctx, asset.CredentialRef)
	if err != nil {
		t.Fatal(err)
	}
	if cred.Password != pass {
		t.Fatalf("resolve failed: %+v", cred)
	}
}

func TestRejectTraversalRef(t *testing.T) {
	if err := secrets.ValidateRef("../etc/passwd"); err == nil {
		t.Fatal("expected error")
	}
	if err := secrets.ValidateRef(""); err == nil {
		t.Fatal("expected error")
	}
}
