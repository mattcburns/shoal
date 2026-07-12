// Package prompts embeds versioned prompt and schema assets for Core AI.
package prompts

import (
	"embed"
	"fmt"
)

//go:embed schemas/normalization_result.v1.json
//go:embed reconcile_asset.v1.md
//go:embed fewshot/reconcile_asset.v1.jsonl
var fs embed.FS

// Assets is the set of files used by reconcile.
type Assets struct {
	SchemaNormalizationResult string
	ReconcileAssetMD          string
	ReconcileAssetFewShot     string
}

// Load reads embedded prompt files.
func Load() (Assets, error) {
	schema, err := fs.ReadFile("schemas/normalization_result.v1.json")
	if err != nil {
		return Assets{}, fmt.Errorf("prompts: schema: %w", err)
	}
	md, err := fs.ReadFile("reconcile_asset.v1.md")
	if err != nil {
		return Assets{}, fmt.Errorf("prompts: reconcile_asset: %w", err)
	}
	few, err := fs.ReadFile("fewshot/reconcile_asset.v1.jsonl")
	if err != nil {
		return Assets{}, fmt.Errorf("prompts: fewshot: %w", err)
	}
	return Assets{
		SchemaNormalizationResult: string(schema),
		ReconcileAssetMD:          string(md),
		ReconcileAssetFewShot:     string(few),
	}, nil
}
