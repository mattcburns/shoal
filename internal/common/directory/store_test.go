package directory

import (
	"context"
	"testing"

	"github.com/mattcburns/shoal/internal/common/models"
)

func TestFileStoreConformance(t *testing.T) {
	RunConformance(t, func() Store {
		fs, err := NewFileStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewFileStore: %v", err)
		}
		return fs
	})
}

func TestNewFileStoreRejectsEmptyDir(t *testing.T) {
	if _, err := NewFileStore(""); err == nil {
		t.Fatalf("NewFileStore(\"\") = nil error, want error")
	}
	if _, err := NewFileStore("   "); err == nil {
		t.Fatalf("NewFileStore(whitespace) = nil error, want error")
	}
}

func TestFileStorePersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	fs1, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	ctx := context.Background()
	id, err := fs1.UpsertDevice(ctx, models.DeviceIdentity{Serial: "SN-RELOAD", Name: "node-reload"})
	if err != nil {
		t.Fatalf("UpsertDevice: %v", err)
	}

	fs2, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore (reload): %v", err)
	}
	got, err := fs2.GetDevice(ctx, id)
	if err != nil {
		t.Fatalf("GetDevice after reload: %v", err)
	}
	if got.Serial != "SN-RELOAD" {
		t.Fatalf("GetDevice after reload = %+v, want Serial=SN-RELOAD", got)
	}
}
