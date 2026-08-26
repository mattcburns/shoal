package discover

import (
	"fmt"
	"strings"

	"github.com/mattcburns/shoal/internal/common/models"
)

// RawAssetInput checks kind and payload presence.
func RawAssetInput(in models.RawAssetInput) error {
	switch strings.ToLower(in.Kind) {
	case "redfish_json":
		if len(in.RedfishJSON) == 0 {
			return fmt.Errorf("validate: redfish_json payload is required")
		}
	case "csv":
		if len(in.CSVRow) == 0 {
			return fmt.Errorf("validate: csv_row payload is required")
		}
	case "photo":
		if strings.TrimSpace(in.PhotoBase64) == "" {
			return fmt.Errorf("validate: photo_base64 payload is required")
		}
	default:
		return fmt.Errorf("validate: unknown raw asset kind %q", in.Kind)
	}
	return nil
}
