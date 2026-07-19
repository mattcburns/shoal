package job

import (
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
)

func TestApplyProfileDefaults(t *testing.T) {
	p := models.ProvisioningProfile{
		Ref:             "p1",
		ISOBase:         "shoal-marker",
		InstallStrategy: models.InstallStrategyImageWrite,
		OSFamily:        models.OSFamilyUbuntu,
		SeedDelivery:    models.SeedDeliveryNone,
		Prep:            "skip",
		MediaURL:        "http://lab:8080/from-profile.iso",
		StageTimeout:    "45m",
	}
	req := models.StartJobRequest{DeviceID: "d1"}
	applyProfileDefaults(&req, p)
	if req.InstallStrategy != models.InstallStrategyImageWrite {
		t.Fatalf("strategy=%s", req.InstallStrategy)
	}
	if req.OsFamily != models.OSFamilyUbuntu {
		t.Fatalf("family=%s", req.OsFamily)
	}
	if req.ISOURL != "http://lab:8080/from-profile.iso" {
		t.Fatalf("iso=%s", req.ISOURL)
	}
	if req.StageTimeout != 45*time.Minute {
		t.Fatalf("timeout=%s", req.StageTimeout)
	}
	// request wins
	req2 := models.StartJobRequest{
		InstallStrategy: models.InstallStrategySimulate,
		ISOURL:          "http://lab:8080/override.iso",
	}
	applyProfileDefaults(&req2, p)
	if req2.InstallStrategy != models.InstallStrategySimulate {
		t.Fatal("request should win strategy")
	}
	if req2.ISOURL != "http://lab:8080/override.iso" {
		t.Fatal("request should win iso")
	}
}

func TestResolveProfileURLs(t *testing.T) {
	p := models.ProvisioningProfile{
		ISOBase:     "shoal-marker",
		SeedISOBase: "cidata",
		PrepISOBase: "shoal-prep",
	}
	req := models.StartJobRequest{}
	if err := resolveProfileURLs(&req, p, "http://192.168.124.1:8080"); err != nil {
		t.Fatal(err)
	}
	if req.ISOURL != "http://192.168.124.1:8080/shoal-marker.iso" {
		t.Fatalf("iso=%s", req.ISOURL)
	}
	if req.SeedISOURL != "http://192.168.124.1:8080/cidata.iso" {
		t.Fatalf("seed=%s", req.SeedISOURL)
	}
	if req.PrepISOURL != "http://192.168.124.1:8080/shoal-prep.iso" {
		t.Fatalf("prep=%s", req.PrepISOURL)
	}
}
