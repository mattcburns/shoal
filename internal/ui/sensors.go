package ui

import (
	"net/http"
	"strconv"
	"time"

	"github.com/mattcburns/shoal/internal/common/telemetry"
)

// sensorRow is the display-ready form of a telemetry.SensorReading (pointer
// fields resolved to strings so the template doesn't need to dereference).
type sensorRow struct {
	Sensor string
	Value  string
	Unit   string
	Note   string
	TS     string
}

type sensorsPageData struct {
	DeviceID    string
	BMCEndpoint string
	Error       string
	PollMessage string
	PollError   bool
	Sensors     []sensorRow
}

// handleSensorsGet is GET /ui/devices/{id}/sensors: a "Poll BMC" form plus a
// flat sensor-readings table, mirroring internal/api/devices.go's
// handleDeviceSensors (same Observe.ListSensors call, same ?limit=
// pagination convention, default+cap at maxListLimit).
func (s *Server) handleSensorsGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing device id", http.StatusBadRequest)
		return
	}
	q := r.URL.Query()
	data := sensorsPageData{
		DeviceID:    id,
		BMCEndpoint: q.Get("bmc_endpoint"),
	}
	applyPollFeedback(&data.PollMessage, &data.PollError, q)

	if s.Observe == nil {
		data.Error = "Observe not configured (set SHOAL_TELEMETRY_DATABASE_URL)."
		s.renderPage(w, r, "sensors.html", data)
		return
	}

	limit := parseLimit(r, maxListLimit, maxListLimit)
	var since time.Time
	if v := q.Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			since = t
		}
	}
	readings, err := s.Observe.ListSensors(r.Context(), id, since, limit)
	if err != nil {
		if isNotConfiguredErr(err) {
			data.Error = "Observe not configured (set SHOAL_TELEMETRY_DATABASE_URL)."
		} else {
			s.logErr("ui device sensors", err, "device_id", id)
			data.Error = "Sensor lookup failed (see server logs)."
		}
	}
	data.Sensors = make([]sensorRow, 0, len(readings))
	for _, rd := range readings {
		data.Sensors = append(data.Sensors, toSensorRow(rd))
	}
	s.renderPage(w, r, "sensors.html", data)
}

// handleSensorsPoll is POST /ui/devices/{id}/sensors: the "Poll BMC" form
// submit target for this tab.
func (s *Server) handleSensorsPoll(w http.ResponseWriter, r *http.Request) {
	s.handlePollForm(w, r, "sensors")
}

func toSensorRow(rd telemetry.SensorReading) sensorRow {
	row := sensorRow{
		Sensor: rd.Sensor,
		Unit:   rd.Unit,
		Note:   rd.Note,
	}
	if rd.Value != nil {
		row.Value = strconv.FormatFloat(*rd.Value, 'g', -1, 64)
	}
	if !rd.TS.IsZero() {
		row.TS = rd.TS.Format(time.RFC3339)
	}
	return row
}
