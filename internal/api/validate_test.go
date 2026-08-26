package api

import "testing"

func TestDeviceCredentials(t *testing.T) {
	if err := validateDeviceCredentials("root", "x"); err != nil {
		t.Fatal(err)
	}
	if err := validateDeviceCredentials("root", ""); err != nil {
		t.Fatal(err)
	}
	if err := validateDeviceCredentials("", "x"); err == nil {
		t.Fatal("expected username required")
	}
}

func TestDevicePoll(t *testing.T) {
	if err := validateDevicePoll("https://bmc"); err != nil {
		t.Fatal(err)
	}
	if err := validateDevicePoll(""); err == nil {
		t.Fatal("expected missing endpoint")
	}
}

func TestDevicePower(t *testing.T) {
	if err := validateDevicePower("On", "https://bmc"); err != nil {
		t.Fatal(err)
	}
	if err := validateDevicePower("Explode", "https://bmc"); err == nil {
		t.Fatal("expected bad reset_type")
	}
	if err := validateDevicePower("On", ""); err == nil {
		t.Fatal("expected missing endpoint")
	}
}
