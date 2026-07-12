package reconcile_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/core/ai"
	"github.com/mattcburns/shoal/internal/core/reconcile"
)

func TestReconcileAssetWithFake(t *testing.T) {
	fake := &ai.Fake{
		Content: `{
  "asset": {"serial":"SN1","model":"M","vendor":"V","bmc_ip":"10.0.0.1"},
  "confidences": [
    {"field":"serial","confidence":0.9,"source":"ai","evidence":"SerialNumber SN1"},
    {"field":"bmc_ip","confidence":0.9,"source":"ai","evidence":"10.0.0.1"}
  ],
  "needs_review": false
}`,
	}
	svc, err := reconcile.New(fake, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.ReconcileAsset(context.Background(), reconcile.ReconcileAssetInput{
		RedactedRaw: map[string]any{"SerialNumber": "SN1", "password": "[REDACTED]"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Asset.Serial != "SN1" {
		t.Fatalf("serial %q", got.Asset.Serial)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("calls %d", len(fake.Calls))
	}
	if strings.Contains(fake.Calls[0].User, "secretpass") {
		t.Fatal("secret leaked into prompt")
	}
}

func TestReconcileAssetRejectsUnredacted(t *testing.T) {
	svc, err := reconcile.New(&ai.Fake{Content: `{}`}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.ReconcileAsset(context.Background(), reconcile.ReconcileAssetInput{
		RedactedRaw: map[string]any{"password": "still-here"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestReconcileAssetPhoto(t *testing.T) {
	fake := &ai.Fake{
		Content: `{
  "asset": {"serial":"CAM1","model":"X","vendor":"Y","bmc_ip":"1.2.3.4"},
  "confidences": [
    {"field":"serial","confidence":0.7,"source":"ai","evidence":"label CAM1"},
    {"field":"bmc_ip","confidence":0.7,"source":"ai","evidence":"hint"}
  ],
  "needs_review": true
}`,
	}
	svc, err := reconcile.New(fake, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.ReconcileAssetPhoto(context.Background(), reconcile.ReconcilePhotoInput{
		Image:     []byte{0xff, 0xd8, 0xff},
		MediaType: "image/jpeg",
		BMCIP:     "1.2.3.4",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Asset.Serial != "CAM1" {
		t.Fatalf("serial %q", got.Asset.Serial)
	}
	if len(fake.VisCalls) != 1 {
		t.Fatal("expected vision call")
	}
}

func TestReconcileEventPassThrough(t *testing.T) {
	svc, err := reconcile.New(&ai.Fake{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ev, err := svc.ReconcileEvent(context.Background(), models.RawEventInput{
		DeviceID: "d1",
		Source:   "sel",
		Message:  "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ev.DeviceID != "d1" {
		t.Fatalf("%+v", ev)
	}
}
