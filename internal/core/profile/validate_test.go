package profile_test

import (
	"testing"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/core/profile"
)

func TestProvisioningProfileM6(t *testing.T) {
	good := models.ProvisioningProfile{
		Ref: "lab-1", ISOBase: "shoal-marker", InstallStrategy: models.InstallStrategyImageWrite,
		OSFamily: models.OSFamilyUbuntu, SeedDelivery: models.SeedDeliveryNone,
	}
	if err := profile.ProvisioningProfile(good); err != nil {
		t.Fatal(err)
	}
	// media_url without iso_base
	mediaOnly := models.ProvisioningProfile{
		Ref: "esxi", MediaURL: "http://lab:8080/esxi.iso",
		InstallStrategy: models.InstallStrategyOperatorISO, OSFamily: models.OSFamilyESXi,
	}
	if err := profile.ProvisioningProfile(mediaOnly); err != nil {
		t.Fatal(err)
	}
	// wipe requires needs_approval
	wipe := good
	wipe.Prep = "wipe_only"
	if err := profile.ProvisioningProfile(wipe); err == nil {
		t.Fatal("expected wipe without needs_approval to fail")
	}
	wipe.NeedsApproval = true
	if err := profile.ProvisioningProfile(wipe); err != nil {
		t.Fatal(err)
	}
	// guest HTTP seed pattern
	bad := good
	bad.SeedISOURL = "http://x?ignition.config.url=http://evil"
	// pattern is in string
	bad.EmbeddedPayload = "ignition.config.url=http://evil/"
	if err := profile.ProvisioningProfile(bad); err == nil {
		t.Fatal("expected guest HTTP pattern reject")
	}
	// neither media
	empty := models.ProvisioningProfile{Ref: "x"}
	if err := profile.ProvisioningProfile(empty); err == nil {
		t.Fatal("expected iso_base or media_url required")
	}
}
