package redfish

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// SystemFixture is a subset of Redfish ComputerSystem JSON used by record/replay tests.
// Fields match common sushy-tools / DMTF shapes (PascalCase JSON).
type SystemFixture struct {
	ID           string `json:"Id"`
	Name         string `json:"Name"`
	SerialNumber string `json:"SerialNumber"`
	Manufacturer string `json:"Manufacturer"`
	Model        string `json:"Model"`
	PowerState   string `json:"PowerState"`
	// ODataID may appear as @odata.id in full documents; optional here.
	ODataID string `json:"@odata.id"`
}

// LoadSystemFixture reads a JSON system document from path.
func LoadSystemFixture(path string) (SystemFixture, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return SystemFixture{}, err
	}
	var f SystemFixture
	if err := json.Unmarshal(b, &f); err != nil {
		return SystemFixture{}, fmt.Errorf("redfish fixture %s: %w", path, err)
	}
	if strings.TrimSpace(f.SerialNumber) == "" && strings.TrimSpace(f.ID) == "" {
		return SystemFixture{}, fmt.Errorf("redfish fixture %s: missing SerialNumber and Id", path)
	}
	return f, nil
}

// ToSystemInfo maps a fixture into the Shoal domain type.
func (f SystemFixture) ToSystemInfo() SystemInfo {
	id := f.ID
	if id == "" {
		id = f.Name
	}
	odata := f.ODataID
	if odata == "" && id != "" {
		odata = "/redfish/v1/Systems/" + id
	}
	return SystemInfo{
		ID:           id,
		Name:         f.Name,
		Serial:       f.SerialNumber,
		Manufacturer: f.Manufacturer,
		Model:        f.Model,
		PowerState:   f.PowerState,
		ODataID:      odata,
	}
}
