package reconcile_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/core/ai"
	"github.com/mattcburns/shoal/internal/core/fewshot"
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
		RedactedRaw: map[string]any{"SerialNumber": "SN1"},
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
}

func TestReconcileAssetIncludesLearnedFewShot(t *testing.T) {
	dir := t.TempDir()
	st, err := fewshot.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.Append(context.Background(), fewshot.Example{
		Prompt: fewshot.PromptReconcileAsset,
		Kind:   "redfish_json",
		Input:  map[string]any{"SerialNumber": "LEARNED"},
		Output: models.NormalizationResult{
			Asset: models.NormalizedAsset{Serial: "LEARNED", BMCIP: "9.9.9.9"},
		},
		Source: "operator_confirm",
	})
	if err != nil {
		t.Fatal(err)
	}
	fake := &ai.Fake{
		ResponseFn: func(req ai.CompletionRequest) (ai.CompletionResponse, error) {
			if !strings.Contains(req.User, "LEARNED") {
				t.Fatalf("prompt missing learned example")
			}
			return ai.CompletionResponse{Content: `{
  "asset": {"serial":"SN2","model":"M","vendor":"V","bmc_ip":"10.0.0.2"},
  "confidences": [
    {"field":"serial","confidence":0.9,"source":"ai","evidence":"x"},
    {"field":"bmc_ip","confidence":0.9,"source":"ai","evidence":"y"}
  ],
  "needs_review": false
}`}, nil
		},
	}
	svc, err := reconcile.NewWithFewShot(fake, nil, st)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.ReconcileAsset(context.Background(), reconcile.ReconcileAssetInput{
		RedactedRaw: map[string]any{"Id": "2"},
	})
	if err != nil {
		t.Fatal(err)
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

func TestReconcileAssetPhotoOCRDirect(t *testing.T) {
	// deepseek-ocr style Free OCR output — no text LLM needed when serial is labeled.
	fake := &ai.Fake{
		VisionFn: func(req ai.VisionRequest) (ai.CompletionResponse, error) {
			if req.User != "Free OCR." {
				t.Fatalf("vision prompt %q", req.User)
			}
			return ai.CompletionResponse{
				Content: "SERIAL: LAB-P3-PHOTO-001\n\nVENDOR Shoal Virtual\n\nMODEL: test-node",
				Model:   "deepseek-ocr",
			}, nil
		},
	}
	svc, err := reconcile.New(fake, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.ReconcileAssetPhoto(context.Background(), reconcile.ReconcilePhotoInput{
		Image:     []byte{0xff, 0xd8, 0xff},
		MediaType: "image/jpeg",
		BMCIP:     "10.77.77.77",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Asset.Serial != "LAB-P3-PHOTO-001" {
		t.Fatalf("serial %q", got.Asset.Serial)
	}
	if got.Asset.Vendor != "Shoal Virtual" || got.Asset.Model != "test-node" {
		t.Fatalf("%+v", got.Asset)
	}
	if got.Asset.BMCIP != "10.77.77.77" {
		t.Fatalf("bmc %q", got.Asset.BMCIP)
	}
	if got.NeedsReview {
		t.Fatal("full OCR should not need review")
	}
	if len(fake.VisCalls) != 1 || len(fake.Calls) != 0 {
		t.Fatalf("vision=%d text=%d", len(fake.VisCalls), len(fake.Calls))
	}
}

func TestReconcileAssetPhotoFailsWithoutSerial(t *testing.T) {
	fake := &ai.Fake{
		VisionFn: func(req ai.VisionRequest) (ai.CompletionResponse, error) {
			return ai.CompletionResponse{Content: "a blurry grey rectangle", Model: "moondream"}, nil
		},
		ResponseFn: func(req ai.CompletionRequest) (ai.CompletionResponse, error) {
			// LLM also cannot invent a serial
			return ai.CompletionResponse{Content: `{
  "asset": {"serial":"","model":"","vendor":"","bmc_ip":"1.2.3.4"},
  "confidences": [{"field":"bmc_ip","confidence":0.9,"source":"ai","evidence":"hint"}],
  "needs_review": true
}`}, nil
		},
	}
	svc, err := reconcile.New(fake, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.ReconcileAssetPhoto(context.Background(), reconcile.ReconcilePhotoInput{
		Image: []byte{1, 2, 3}, BMCIP: "1.2.3.4",
	})
	if err == nil {
		t.Fatal("expected failure without serial")
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
