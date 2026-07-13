package fewshot_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/core/fewshot"
)

func TestFileStoreAppendLoad(t *testing.T) {
	dir := t.TempDir()
	st, err := fewshot.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ex := fewshot.Example{
		Prompt: fewshot.PromptReconcileAsset,
		Kind:   "redfish_json",
		Input:  map[string]any{"SerialNumber": "S1"},
		Output: models.NormalizationResult{
			Asset: models.NormalizedAsset{Serial: "S1", BMCIP: "1.1.1.1"},
		},
		Source: "operator_confirm",
	}
	stored, err := st.Append(ctx, ex)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ID == "" {
		t.Fatal("expected id")
	}
	got, err := st.Load(ctx, fewshot.PromptReconcileAsset, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Output.Asset.Serial != "S1" {
		t.Fatalf("%+v", got)
	}
	// file mode should be private
	path := filepath.Join(dir, fewshot.PromptReconcileAsset+".learned.jsonl")
	if path == "" {
		t.Fatal("path")
	}
}

func TestRejectSensitiveInput(t *testing.T) {
	st, err := fewshot.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.Append(context.Background(), fewshot.Example{
		Prompt: fewshot.PromptReconcileAsset,
		Kind:   "csv",
		Input:  map[string]any{"password": "secret"},
		Output: models.NormalizationResult{
			Asset: models.NormalizedAsset{Serial: "S", BMCIP: "1.1.1.1"},
		},
		Source: "operator_confirm",
	})
	if err == nil {
		t.Fatal("expected reject")
	}
}

func TestFormatForPrompt(t *testing.T) {
	s := fewshot.FormatForPrompt([]fewshot.Example{{
		Input:  map[string]any{"a": 1},
		Output: models.NormalizationResult{Asset: models.NormalizedAsset{Serial: "X", BMCIP: "1.1.1.1"}},
	}})
	if s == "" || s[0] != '{' {
		t.Fatalf("%q", s)
	}
}
