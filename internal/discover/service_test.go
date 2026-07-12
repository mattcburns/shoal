package discover_test

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"testing"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/netbox"
	"github.com/mattcburns/shoal/internal/common/secrets"
	"github.com/mattcburns/shoal/internal/core/ai"
	"github.com/mattcburns/shoal/internal/core/reconcile"
	"github.com/mattcburns/shoal/internal/discover"
)

func TestIngestDeterministic(t *testing.T) {
	nb := netbox.NewMemory()
	sec := secrets.NewMemory()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := discover.New(log, nil, sec, nb)
	got, err := svc.Ingest(context.Background(), models.RawAssetInput{
		Kind: "redfish_json",
		RedfishJSON: map[string]any{
			"SerialNumber": "SN-DET",
			"Manufacturer": "Dell",
			"Model":        "R640",
		},
		BMCIP:       "10.0.0.9",
		BMCUsername: "admin",
		BMCPassword: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.UsedAI {
		t.Fatal("expected deterministic path")
	}
	if got.Asset.CredentialRef == "" {
		t.Fatal("expected credential_ref")
	}
	if got.NetBoxID == "" {
		t.Fatal("expected netbox id")
	}
	// password never on asset
	if got.Asset.Serial != "SN-DET" {
		t.Fatalf("%+v", got.Asset)
	}
}

func TestIngestAIFallback(t *testing.T) {
	fake := &ai.Fake{
		Content: `{
  "asset": {"serial":"SN-AI","model":"X","vendor":"Y","bmc_ip":"10.1.1.1"},
  "confidences": [
    {"field":"serial","confidence":0.8,"source":"ai","evidence":"Id"},
    {"field":"bmc_ip","confidence":0.8,"source":"ai","evidence":"10.1.1.1"}
  ],
  "needs_review": true
}`,
	}
	rec, err := reconcile.New(fake, nil)
	if err != nil {
		t.Fatal(err)
	}
	nb := netbox.NewMemory()
	svc := discover.New(nil, rec, secrets.NewMemory(), nb)
	// Messy dump: no serial → gate fails → AI
	got, err := svc.Ingest(context.Background(), models.RawAssetInput{
		Kind: "redfish_json",
		RedfishJSON: map[string]any{
			"Id":   "1",
			"Name": "weird",
		},
		BMCIP: "10.1.1.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.UsedAI {
		t.Fatal("expected AI path")
	}
	if got.Asset.Serial != "SN-AI" {
		t.Fatalf("%+v", got.Asset)
	}
}

func TestIngestPhotoRedaction(t *testing.T) {
	fake := &ai.Fake{
		Content: `{
  "asset": {"serial":"P1","model":"M","vendor":"V","bmc_ip":"2.2.2.2"},
  "confidences": [
    {"field":"serial","confidence":0.7,"source":"ai","evidence":"photo"},
    {"field":"bmc_ip","confidence":0.7,"source":"ai","evidence":"hint"}
  ],
  "needs_review": false
}`,
	}
	rec, err := reconcile.New(fake, nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := discover.New(nil, rec, secrets.NewMemory(), netbox.NewMemory())
	img := base64.StdEncoding.EncodeToString([]byte{0xff, 0xd8, 0xff, 0x00})
	got, err := svc.Ingest(context.Background(), models.RawAssetInput{
		Kind:        "photo",
		PhotoBase64: img,
		BMCIP:       "2.2.2.2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.UsedAI {
		t.Fatal("photo uses AI")
	}
	if len(fake.VisCalls) != 1 {
		t.Fatal("expected vision call")
	}
}
