package adapters

import (
	"fmt"
	"strings"

	"github.com/mattcburns/shoal/internal/common/models"
)

// CSVAdapter maps known inventory column names.
type CSVAdapter struct{}

// Kind implements Adapter.
func (CSVAdapter) Kind() string { return "csv" }

// Parse maps csv_row keys (case-insensitive) into a partial asset.
func (CSVAdapter) Parse(in models.RawAssetInput) (Partial, error) {
	if strings.ToLower(in.Kind) != "csv" {
		return Partial{}, fmt.Errorf("adapters: kind %q not csv", in.Kind)
	}
	row := in.CSVRow
	if len(row) == 0 {
		return Partial{}, fmt.Errorf("adapters: empty csv_row")
	}
	get := func(names ...string) (string, string) {
		for _, n := range names {
			for k, v := range row {
				if strings.EqualFold(k, n) {
					if s := strings.TrimSpace(v); s != "" {
						return s, k
					}
				}
			}
		}
		return "", ""
	}

	var asset models.NormalizedAsset
	var confs []models.FieldConfidence
	if s, k := get("serial", "serial_number", "SerialNumber"); s != "" {
		asset.Serial = s
		confs = append(confs, conf("serial", 0.95, k+": "+s))
	}
	if s, k := get("vendor", "manufacturer", "make"); s != "" {
		asset.Vendor = s
		confs = append(confs, conf("vendor", 0.9, k+": "+s))
	}
	if s, k := get("model", "sku"); s != "" {
		asset.Model = s
		confs = append(confs, conf("model", 0.9, k+": "+s))
	}
	if ip := strings.TrimSpace(in.BMCIP); ip != "" {
		asset.BMCIP = ip
		confs = append(confs, conf("bmc_ip", 0.99, "operator bmc_ip: "+ip))
	} else if s, k := get("bmc_ip", "bmcip", "ip", "management_ip"); s != "" {
		asset.BMCIP = s
		confs = append(confs, conf("bmc_ip", 0.95, k+": "+s))
	}
	return Partial{Asset: asset, Confidences: confs}, nil
}
