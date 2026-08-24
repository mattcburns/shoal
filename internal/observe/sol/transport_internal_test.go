package sol

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSolDebugFileCapturesRawBytes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SHOAL_SOL_DEBUG_DIR", dir)

	dbg := solDebugFile("redfish", "https://172.16.21.202")
	if dbg == nil {
		t.Fatal("expected debug file when SHOAL_SOL_DEBUG_DIR set")
	}
	ar := &activityReader{r: strings.NewReader("\x1b[12;34Hpartial-no-newline"), tee: dbg}
	buf := make([]byte, 64)
	for {
		if _, err := ar.Read(buf); err != nil {
			break
		}
	}
	closeIfSet(dbg)

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ReadDir: %v entries=%d", err, len(entries))
	}
	name := entries[0].Name()
	if !strings.HasPrefix(name, "sol-redfish-https___172.16.21.202-") {
		t.Fatalf("unexpected capture name %q", name)
	}
	got, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "\x1b[12;34Hpartial-no-newline" {
		t.Fatalf("capture = %q", got)
	}
}

func TestSolDebugFileDisabledByDefault(t *testing.T) {
	t.Setenv("SHOAL_SOL_DEBUG_DIR", "")
	if dbg := solDebugFile("redfish", "x"); dbg != nil {
		t.Fatal("expected nil debug file when env empty")
	}
}
