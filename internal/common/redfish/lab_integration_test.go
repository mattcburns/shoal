//go:build integration

package redfish_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/common/redfish"
)

// Live lab (VM mode): sushy-tools on shoal_lab_vm_host:8001.
//
//	SHOAL_BMC_URL=http://192.168.122.100:8001 \
//	SHOAL_BMC_USERNAME=... SHOAL_BMC_PASSWORD=... \
//	go test ./internal/common/redfish -tags=integration -count=1 -v
func TestLabRedfishVirtualMediaBootCleanup(t *testing.T) {
	base := envOr("SHOAL_BMC_URL", "http://192.168.122.100:8001")
	user := os.Getenv("SHOAL_BMC_USERNAME")
	pass := os.Getenv("SHOAL_BMC_PASSWORD")
	if user == "" || pass == "" {
		t.Skip("SHOAL_BMC_USERNAME/PASSWORD required")
	}
	iso := envOr("SHOAL_ISO_URL", "http://192.168.124.1:8080/shoal-marker.iso")
	// Prefer node-1 if present; else first system.
	wantName := envOr("SHOAL_SYSTEM_NAME", "shoal-node-1")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	bmc, err := redfish.NewBMC(redfish.Config{
		BaseURL:       base,
		Username:      user,
		Password:      pass,
		AuthMode:      "basic",
		TLSMode:       "off",
		MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := bmc.Open(ctx); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = bmc.Close(context.Background()) }()

	systems, err := bmc.ListSystems(ctx)
	if err != nil {
		t.Fatalf("list systems: %v", err)
	}
	if len(systems) == 0 {
		t.Fatal("no systems")
	}
	sysID := systems[0].ID
	for _, s := range systems {
		if s.Name == wantName {
			sysID = s.ID
			break
		}
	}
	t.Logf("using system id=%s", sysID)

	vms, err := bmc.ListVirtualMedia(ctx, sysID)
	if err != nil {
		t.Fatalf("list vm: %v", err)
	}
	var mediaURI string
	for _, vm := range vms {
		if vm.SupportsCD {
			mediaURI = vm.URI
			break
		}
	}
	if mediaURI == "" {
		t.Fatalf("no CD media in %#v", vms)
	}
	t.Logf("media URI=%s iso=%s", mediaURI, iso)

	if err := bmc.InsertVirtualMedia(ctx, mediaURI, iso); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := bmc.SetBootOverrideOnceCD(ctx, sysID); err != nil {
		t.Fatalf("boot override: %v", err)
	}
	boot, err := bmc.GetBoot(ctx, sysID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boot after set: %+v", boot)
	if boot.OverrideTarget != "Cd" && boot.OverrideTarget != "CD" {
		// sushy uses "Cd"
		t.Logf("warning: override target is %q (expected Cd)", boot.OverrideTarget)
	}

	if err := bmc.Power(ctx, sysID, "On"); err != nil {
		t.Fatalf("power: %v", err)
	}

	// Mandatory cleanup
	if err := bmc.CleanupMediaAndBoot(ctx, sysID); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	boot, _ = bmc.GetBoot(ctx, sysID)
	vms, _ = bmc.ListVirtualMedia(ctx, sysID)
	for _, vm := range vms {
		if vm.URI == mediaURI && vm.Inserted {
			t.Fatal("media still inserted after cleanup")
		}
	}
	t.Logf("boot after cleanup: %+v", boot)
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
