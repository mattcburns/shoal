package redfish

import (
	"context"
	"testing"
)

func TestKeepFirmwareInventory(t *testing.T) {
	if !keepFirmwareInventory("Installed-0-BIOS", "BIOS") {
		t.Fatal("installed bios")
	}
	if keepFirmwareInventory("Available-0-BIOS", "BIOS") {
		t.Fatal("skip available")
	}
	if keepFirmwareInventory("Previous-1-iDRAC", "iDRAC") {
		t.Fatal("skip previous")
	}
	if keepFirmwareInventory("Current-159-1.15.2__BIOS.Setup.1-1", "BIOS") {
		t.Fatal("skip current")
	}
}

func TestFakeListFirmware(t *testing.T) {
	f := NewFake()
	f.Firmware = []FirmwareComponent{
		{ID: "Installed-0-BIOS", Name: "BIOS", Version: "1.2.3"},
		{ID: "Available-0-BIOS", Name: "BIOS", Version: "1.2.4"},
	}
	got, err := f.ListFirmware(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("fake does not filter: %+v", got)
	}
}
