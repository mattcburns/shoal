// Package prompts embeds versioned prompt and schema assets for Core AI.
package prompts

import (
	"embed"
	"fmt"
)

//go:embed schemas/normalization_result.v1.json
//go:embed schemas/provisioning_profile.v1.json
//go:embed schemas/failure_screen_ocr.v1.json
//go:embed reconcile_asset.v1.md
//go:embed provisioning_profile.v1.md
//go:embed failure_screen_ocr.v1.md
//go:embed fewshot/reconcile_asset.v1.jsonl
//go:embed fewshot/provisioning_profile.v1.jsonl
//go:embed fewshot/failure_screen_ocr.v1.jsonl
var fs embed.FS

// Assets is the set of files used by reconcile, profile generation, and OCR.
type Assets struct {
	SchemaNormalizationResult  string
	SchemaProvisioningProfile  string
	SchemaFailureScreenOCR     string
	ReconcileAssetMD           string
	ReconcileAssetFewShot      string
	ProvisioningProfileMD      string
	ProvisioningProfileFewShot string
	FailureScreenOCRMD         string
	FailureScreenOCRFewShot    string
}

// Load reads embedded prompt files.
func Load() (Assets, error) {
	schema, err := fs.ReadFile("schemas/normalization_result.v1.json")
	if err != nil {
		return Assets{}, fmt.Errorf("prompts: schema normalization: %w", err)
	}
	profSchema, err := fs.ReadFile("schemas/provisioning_profile.v1.json")
	if err != nil {
		return Assets{}, fmt.Errorf("prompts: schema profile: %w", err)
	}
	md, err := fs.ReadFile("reconcile_asset.v1.md")
	if err != nil {
		return Assets{}, fmt.Errorf("prompts: reconcile_asset: %w", err)
	}
	few, err := fs.ReadFile("fewshot/reconcile_asset.v1.jsonl")
	if err != nil {
		return Assets{}, fmt.Errorf("prompts: fewshot asset: %w", err)
	}
	pmd, err := fs.ReadFile("provisioning_profile.v1.md")
	if err != nil {
		return Assets{}, fmt.Errorf("prompts: provisioning_profile: %w", err)
	}
	pfew, err := fs.ReadFile("fewshot/provisioning_profile.v1.jsonl")
	if err != nil {
		return Assets{}, fmt.Errorf("prompts: fewshot profile: %w", err)
	}
	ocrSchema, err := fs.ReadFile("schemas/failure_screen_ocr.v1.json")
	if err != nil {
		return Assets{}, fmt.Errorf("prompts: schema failure_screen_ocr: %w", err)
	}
	ocrMD, err := fs.ReadFile("failure_screen_ocr.v1.md")
	if err != nil {
		return Assets{}, fmt.Errorf("prompts: failure_screen_ocr: %w", err)
	}
	ocrFew, err := fs.ReadFile("fewshot/failure_screen_ocr.v1.jsonl")
	if err != nil {
		return Assets{}, fmt.Errorf("prompts: fewshot failure_screen_ocr: %w", err)
	}
	return Assets{
		SchemaNormalizationResult:  string(schema),
		SchemaProvisioningProfile:  string(profSchema),
		SchemaFailureScreenOCR:     string(ocrSchema),
		ReconcileAssetMD:           string(md),
		ReconcileAssetFewShot:      string(few),
		ProvisioningProfileMD:      string(pmd),
		ProvisioningProfileFewShot: string(pfew),
		FailureScreenOCRMD:         string(ocrMD),
		FailureScreenOCRFewShot:    string(ocrFew),
	}, nil
}
