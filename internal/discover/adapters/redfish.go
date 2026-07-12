package adapters

import (
	"fmt"
	"strings"

	"github.com/mattcburns/shoal/internal/common/models"
)

// RedfishAdapter parses a Redfish System (or similar) JSON object map.
type RedfishAdapter struct{}

// Kind implements Adapter.
func (RedfishAdapter) Kind() string { return "redfish_json" }

// Parse extracts identity fields from common Redfish property names.
func (RedfishAdapter) Parse(in models.RawAssetInput) (Partial, error) {
	if strings.ToLower(in.Kind) != "redfish_json" {
		return Partial{}, fmt.Errorf("adapters: kind %q not redfish_json", in.Kind)
	}
	m := in.RedfishJSON
	if len(m) == 0 {
		return Partial{}, fmt.Errorf("adapters: empty redfish_json")
	}

	var asset models.NormalizedAsset
	var confs []models.FieldConfidence

	if s, key := firstString(m, "SerialNumber", "serial", "Serial"); s != "" {
		asset.Serial = s
		confs = append(confs, conf("serial", 0.95, key+": "+s))
	}
	if s, key := firstString(m, "Manufacturer", "manufacturer", "Vendor", "vendor"); s != "" {
		asset.Vendor = s
		confs = append(confs, conf("vendor", 0.9, key+": "+s))
	}
	if s, key := firstString(m, "Model", "model", "SKU", "PartNumber"); s != "" {
		asset.Model = s
		confs = append(confs, conf("model", 0.9, key+": "+s))
	}
	// BMC IP: operator hint first, then common keys / nested Managers.
	if ip := strings.TrimSpace(in.BMCIP); ip != "" {
		asset.BMCIP = ip
		confs = append(confs, conf("bmc_ip", 0.99, "operator bmc_ip: "+ip))
	} else if s, key := firstString(m, "BMCIP", "bmc_ip", "HostName"); s != "" && looksLikeIP(s) {
		asset.BMCIP = s
		confs = append(confs, conf("bmc_ip", 0.7, key+": "+s))
	} else if ip := firstIPInMap(m); ip != "" {
		asset.BMCIP = ip
		confs = append(confs, conf("bmc_ip", 0.65, "address: "+ip))
	}

	return Partial{Asset: asset, Confidences: confs}, nil
}

func looksLikeIP(s string) bool {
	// loose: contains a dot and digits
	return strings.Contains(s, ".") && strings.IndexFunc(s, func(r rune) bool {
		return r >= '0' && r <= '9'
	}) >= 0
}

func firstIPInMap(m map[string]any) string {
	for k, v := range m {
		switch t := v.(type) {
		case string:
			if looksLikeIP(t) && (strings.Contains(strings.ToLower(k), "addr") ||
				strings.Contains(strings.ToLower(k), "ip")) {
				return strings.TrimSpace(t)
			}
		case []any:
			for _, el := range t {
				if s, ok := el.(string); ok && looksLikeIP(s) {
					return strings.TrimSpace(s)
				}
			}
		case map[string]any:
			if ip := firstIPInMap(t); ip != "" {
				return ip
			}
		}
	}
	return ""
}
