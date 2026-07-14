package ai

import (
	"strings"

	"github.com/mattcburns/shoal/prompts"
)

// BuildReconcileAssetPrompt fills the reconcile template placeholders.
func BuildReconcileAssetPrompt(template, schema, fewshot, rawJSON, partialJSON string) string {
	s := template
	s = strings.ReplaceAll(s, "{{SCHEMA}}", schema)
	s = strings.ReplaceAll(s, "{{FEWSHOT}}", fewshot)
	s = strings.ReplaceAll(s, "{{RAW}}", rawJSON)
	s = strings.ReplaceAll(s, "{{PARTIAL}}", partialJSON)
	return s
}

// BuildProvisioningProfilePrompt fills the profile generation template.
func BuildProvisioningProfilePrompt(template, schema, fewshot, assetJSON, reqJSON string) string {
	s := template
	s = strings.ReplaceAll(s, "{{SCHEMA}}", schema)
	s = strings.ReplaceAll(s, "{{FEWSHOT}}", fewshot)
	s = strings.ReplaceAll(s, "{{ASSET}}", assetJSON)
	s = strings.ReplaceAll(s, "{{REQUIREMENTS}}", reqJSON)
	return s
}

// LoadPromptAssets loads embedded prompts from the prompts package.
func LoadPromptAssets() (prompts.Assets, error) {
	return prompts.Load()
}
