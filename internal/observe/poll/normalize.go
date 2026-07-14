package poll

import (
	"strings"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
	"github.com/mattcburns/shoal/internal/common/redfish"
)

// normalizeSELEntry is the single deterministic SEL → NormalizedEvent path used
// when no Core Reconciler is injected (and as the baseline mapping).
func normalizeSELEntry(deviceID string, e redfish.SELEntry) models.NormalizedEvent {
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = e.ID
	}
	if msg == "" {
		msg = "sel-entry"
	}
	sev := mapSELSeverity(e.Severity, msg)
	comp := strings.TrimSpace(e.SensorType)
	if comp == "" {
		comp = componentFromMessage(msg)
	}
	ts := e.Created
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	return models.NormalizedEvent{
		DeviceID:  deviceID,
		EventType: "sel",
		Severity:  sev,
		Component: comp,
		Message:   msg,
		Timestamp: ts,
	}
}

func mapSELSeverity(bmcSev, msg string) string {
	switch strings.ToLower(strings.TrimSpace(bmcSev)) {
	case "critical":
		return "critical"
	case "warning":
		return "warning"
	case "error":
		return "error"
	case "ok", "info", "informational":
		return "info"
	}
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "critical"), strings.Contains(lower, "fatal"),
		strings.Contains(lower, "failure"), strings.Contains(lower, "failed"):
		return "critical"
	case strings.Contains(lower, "warning"), strings.Contains(lower, "degraded"):
		return "warning"
	case strings.Contains(lower, "error"):
		return "error"
	default:
		return "info"
	}
}

func componentFromMessage(msg string) string {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "temp"), strings.Contains(lower, "thermal"):
		return "thermal"
	case strings.Contains(lower, "fan"):
		return "fan"
	case strings.Contains(lower, "power"), strings.Contains(lower, "psu"), strings.Contains(lower, "voltage"):
		return "power"
	case strings.Contains(lower, "memory"), strings.Contains(lower, "dimm"):
		return "memory"
	case strings.Contains(lower, "cpu"), strings.Contains(lower, "processor"):
		return "cpu"
	case strings.Contains(lower, "disk"), strings.Contains(lower, "drive"):
		return "storage"
	default:
		return ""
	}
}
