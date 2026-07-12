package adapters_test

import (
	"testing"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/discover/adapters"
)

func TestRedfishCleanDump(t *testing.T) {
	a := adapters.RedfishAdapter{}
	p, err := a.Parse(models.RawAssetInput{
		Kind: "redfish_json",
		RedfishJSON: map[string]any{
			"SerialNumber": "SN-1",
			"Manufacturer": "Dell",
			"Model":        "R640",
		},
		BMCIP: "192.168.122.100",
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Asset.Serial != "SN-1" || p.Asset.BMCIP != "192.168.122.100" {
		t.Fatalf("%+v", p.Asset)
	}
	if len(p.Confidences) < 2 {
		t.Fatalf("confidences %d", len(p.Confidences))
	}
}

func TestCSVParse(t *testing.T) {
	a := adapters.CSVAdapter{}
	p, err := a.Parse(models.RawAssetInput{
		Kind: "csv",
		CSVRow: map[string]string{
			"serial": "S2",
			"bmc_ip": "10.0.0.2",
			"vendor": "HPE",
			"model":  "DL380",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Asset.Vendor != "HPE" {
		t.Fatalf("%+v", p.Asset)
	}
}
