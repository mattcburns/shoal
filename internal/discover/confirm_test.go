package discover_test

import (
	"context"
	"testing"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/netbox"
	"github.com/mattcburns/shoal/internal/common/secrets"
	"github.com/mattcburns/shoal/internal/core/fewshot"
	"github.com/mattcburns/shoal/internal/discover"
)

func TestConfirmLearns(t *testing.T) {
	st, err := fewshot.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := discover.NewWithFewShot(nil, nil, secrets.NewMemory(), netbox.NewMemory(), st)
	got, err := svc.Confirm(context.Background(), discover.ConfirmRequest{
		Kind:  "redfish_json",
		Input: map[string]any{"SerialNumber": "LEARN-1", "Name": "node"},
		Result: models.NormalizationResult{
			Asset: models.NormalizedAsset{Serial: "LEARN-1", BMCIP: "10.0.0.1", Vendor: "Dell"},
			Confidences: []models.FieldConfidence{
				{Field: "serial", Confidence: 0.9, Source: "ai", Evidence: "SN"},
				{Field: "bmc_ip", Confidence: 0.9, Source: "ai", Evidence: "ip"},
			},
			NeedsReview: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Learned || got.ID == "" {
		t.Fatalf("%+v", got)
	}
	loaded, err := st.Load(context.Background(), fewshot.PromptReconcileAsset, 5)
	if err != nil || len(loaded) != 1 {
		t.Fatalf("%v %d", err, len(loaded))
	}
	if loaded[0].Output.NeedsReview {
		t.Fatal("stored output should clear needs_review")
	}
}

func TestConfirmRequiresStore(t *testing.T) {
	svc := discover.New(nil, nil, secrets.NewMemory(), netbox.NewMemory())
	_, err := svc.Confirm(context.Background(), discover.ConfirmRequest{
		Kind:  "csv",
		Input: map[string]any{"serial": "x"},
		Result: models.NormalizationResult{
			Asset: models.NormalizedAsset{Serial: "x", BMCIP: "1.1.1.1"},
		},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestConfirmRejectsSecrets(t *testing.T) {
	st, _ := fewshot.NewFileStore(t.TempDir())
	svc := discover.NewWithFewShot(nil, nil, secrets.NewMemory(), netbox.NewMemory(), st)
	// redact.Map replaces password with [REDACTED] which is not a sensitive *value* key issue —
	// ContainsSensitiveKey checks keys; after Map, key still exists with [REDACTED] value.
	// redact.ContainsSensitiveKey returns true if sensitive keys are present even if redacted.
	_, err := svc.Confirm(context.Background(), discover.ConfirmRequest{
		Kind:  "redfish_json",
		Input: map[string]any{"password": "still-secret"},
		Result: models.NormalizationResult{
			Asset: models.NormalizedAsset{Serial: "x", BMCIP: "1.1.1.1"},
		},
	})
	if err == nil {
		t.Fatal("expected reject")
	}
}
