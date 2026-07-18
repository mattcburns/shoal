package redfish_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mattcburns/shoal/internal/common/redfish"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	// Walk up from cwd for testdata/redfish (works under go test module root).
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := wd
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, "testdata", "redfish")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("testdata/redfish not found from ", wd)
	return ""
}

func TestLoadSystemFixtures(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "testdata", "redfish")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var loaded int
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		// Only system-shaped fixtures (name contains system or sushy_system).
		name := e.Name()
		if !(contains(name, "system") || contains(name, "System")) {
			continue
		}
		path := filepath.Join(dir, name)
		f, err := redfish.LoadSystemFixture(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		info := f.ToSystemInfo()
		if info.Serial == "" && info.ID == "" {
			t.Fatalf("%s: empty identity after map", name)
		}
		if info.ODataID == "" {
			t.Fatalf("%s: expected ODataID", name)
		}
		loaded++
		t.Logf("fixture %s -> serial=%q vendor=%q model=%q", name, info.Serial, info.Manufacturer, info.Model)
	}
	if loaded == 0 {
		t.Fatal("no system fixtures loaded")
	}
}

func contains(s, sub string) bool {
	return stringIndex(s, sub) >= 0
}

func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
