package redfish

import (
	"context"
	"fmt"
	"strings"
)

const maxFirmwareEntries = 200

// ListFirmware returns installed firmware/software inventory from UpdateService.
func (c *client) ListFirmware(_ context.Context) ([]FirmwareComponent, error) {
	api, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	us, err := fetchOne[rfUpdateService](api, c.root.UpdateService.ODataID)
	if err != nil || us == nil {
		return nil, nil
	}
	items, err := fetchCollection[rfSoftwareInventory](api, us.FirmwareInventory.ODataID)
	if err != nil {
		return nil, fmt.Errorf("redfish: firmware inventory: %w", err)
	}
	if len(items) == 0 {
		sw, swErr := fetchCollection[rfSoftwareInventory](api, us.SoftwareInventory.ODataID)
		if swErr == nil {
			items = sw
		}
	}
	out := make([]FirmwareComponent, 0, len(items))
	seen := map[string]struct{}{}
	for _, inv := range items {
		if inv == nil {
			continue
		}
		row := mapSoftwareInventory(inv)
		if !keepFirmwareInventory(row.ID, row.Name) {
			continue
		}
		key := strings.ToLower(row.ID)
		if key == "" {
			key = strings.ToLower(row.Name + "|" + row.Version)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, row)
		if len(out) >= maxFirmwareEntries {
			break
		}
	}
	return out, nil
}

func mapSoftwareInventory(inv *rfSoftwareInventory) FirmwareComponent {
	id := inv.ID
	if id == "" {
		id = inv.ODataID
	}
	return FirmwareComponent{
		ID:           id,
		Name:         inv.Name,
		Version:      inv.Version,
		SoftwareID:   inv.SoftwareID,
		Manufacturer: inv.Manufacturer,
		ReleaseDate:  inv.ReleaseDate,
		Health:       inv.Status.Health,
		State:        inv.Status.State,
		Updateable:   inv.Updateable,
	}
}

func keepFirmwareInventory(id, name string) bool {
	idLower := strings.ToLower(id)
	blob := idLower + " " + strings.ToLower(name)
	if strings.Contains(blob, "available") || strings.Contains(blob, "previous") {
		return false
	}
	// Dell lists both Current-* and Installed-* for the same image.
	if strings.HasPrefix(idLower, "current-") {
		return false
	}
	return true
}
