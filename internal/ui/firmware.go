package ui

import (
	"net/http"
	"time"

	"github.com/mattcburns/shoal/internal/common/telemetry"
)

// firmwareRow is the display-ready form of a telemetry.FirmwareComponent.
type firmwareRow struct {
	Name         string
	Version      string
	Manufacturer string
	Health       string
	Updateable   bool
	ID           string
}

type firmwarePageData struct {
	DeviceID    string
	BMCEndpoint string
	Error       string
	PollMessage string
	PollError   bool
	AsOf        string
	Firmware    []firmwareRow
}

// handleFirmwareGet is GET /ui/devices/{id}/firmware: a "Poll BMC" form plus
// a firmware-inventory table, mirroring internal/api/devices.go's
// handleDeviceFirmware (same Observe.ListFirmware call, same ?limit=
// pagination convention, default+cap at maxListLimit, "as of" timestamp
// taken from the newest row). Gather-only: no flashing/update actions.
func (s *Server) handleFirmwareGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing device id", http.StatusBadRequest)
		return
	}
	q := r.URL.Query()
	data := firmwarePageData{
		DeviceID:    id,
		BMCEndpoint: q.Get("bmc_endpoint"),
	}
	applyPollFeedback(&data.PollMessage, &data.PollError, q)

	if s.Observe == nil {
		data.Error = "Observe not configured (set SHOAL_TELEMETRY_DATABASE_URL)."
		s.renderPage(w, r, "firmware.html", data)
		return
	}

	limit := parseLimit(r, maxListLimit, maxListLimit)
	comps, err := s.Observe.ListFirmware(r.Context(), id, limit)
	if err != nil {
		if isNotConfiguredErr(err) {
			data.Error = "Observe not configured (set SHOAL_TELEMETRY_DATABASE_URL)."
		} else {
			s.logErr("ui device firmware", err, "device_id", id)
			data.Error = "Firmware lookup failed (see server logs)."
		}
	}
	if len(comps) > 0 && !comps[0].TS.IsZero() {
		data.AsOf = comps[0].TS.Format(time.RFC3339)
	}
	data.Firmware = make([]firmwareRow, 0, len(comps))
	for _, c := range comps {
		data.Firmware = append(data.Firmware, toFirmwareRow(c))
	}
	s.renderPage(w, r, "firmware.html", data)
}

// handleFirmwarePoll is POST /ui/devices/{id}/firmware: the "Poll BMC" form
// submit target for this tab.
func (s *Server) handleFirmwarePoll(w http.ResponseWriter, r *http.Request) {
	s.handlePollForm(w, r, "firmware")
}

func toFirmwareRow(c telemetry.FirmwareComponent) firmwareRow {
	name := c.Name
	if name == "" {
		name = c.ID
	}
	return firmwareRow{
		Name:         name,
		Version:      c.Version,
		Manufacturer: c.Manufacturer,
		Health:       c.Health,
		Updateable:   c.Updateable,
		ID:           c.ID,
	}
}
