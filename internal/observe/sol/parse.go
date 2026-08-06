// Package sol implements SOL transports and SHOAL| marker parsing.
package sol

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mattcburns/shoal/internal/common/models"
)

// Supported schema version for the SHOAL marker protocol.
const SchemaVersion = 1

// Marker line format:
//
//	SHOAL|<schema_ver>|<seq>|<iso8601_utc>|<phase>|<percent>|<state>|<detail>
//
// Example:
//
//	SHOAL|1|41|2026-06-19T04:10:11Z|IMAGE_WRITE|65|OK|writing rootfs
func ParseLine(line string) (models.SOLMarker, bool) {
	line = strings.TrimSpace(line)
	// Allow noise before the marker on the same line (console prompts).
	if idx := strings.Index(line, "SHOAL|"); idx >= 0 {
		line = line[idx:]
	}
	if !strings.HasPrefix(line, "SHOAL|") {
		return models.SOLMarker{}, false
	}
	// Split into at most 7 fields so detail may contain '|'.
	parts := strings.SplitN(line, "|", 7)
	if len(parts) < 7 {
		return models.SOLMarker{}, false
	}
	ver, err := strconv.Atoi(parts[1])
	if err != nil || ver != SchemaVersion {
		return models.SOLMarker{}, false
	}
	seq, err := strconv.Atoi(parts[2])
	if err != nil {
		return models.SOLMarker{}, false
	}
	ts, err := time.Parse(time.RFC3339, parts[3])
	if err != nil {
		// try without seconds fraction variants
		ts, err = time.Parse("2006-01-02T15:04:05Z", parts[3])
		if err != nil {
			return models.SOLMarker{}, false
		}
	}
	phase := parts[4]
	var percent *int
	if parts[5] != "" && parts[5] != "-" {
		p, err := strconv.Atoi(parts[5])
		if err != nil {
			return models.SOLMarker{}, false
		}
		percent = &p
	}
	// SplitN(..., 7): parts[6] is "state" or "state|detail..."
	state, detail, _ := strings.Cut(parts[6], "|")

	switch state {
	case "OK", "WARN", "ERROR", "HEARTBEAT":
	default:
		return models.SOLMarker{}, false
	}

	return models.SOLMarker{
		SchemaVer: ver,
		Seq:       seq,
		Timestamp: ts.UTC(),
		Phase:     phase,
		Percent:   percent,
		State:     state,
		Detail:    detail,
	}, true
}

// IsTerminal reports whether the marker should end the provisioning job.
func IsTerminal(m models.SOLMarker) bool {
	if m.State == "ERROR" {
		return true
	}
	if strings.EqualFold(m.Phase, "ERROR") {
		return true
	}
	if strings.EqualFold(m.Phase, "DONE") && m.State == "OK" {
		return true
	}
	return false
}

// IsStageComplete reports whether the marker ends the current stage's SOL session
// without ending the job (e.g. multi-stage PREP_DONE). Watch must stop cleanly
// so Unregister/stream close does not surface as a transport error.
func IsStageComplete(m models.SOLMarker) bool {
	if IsTerminal(m) {
		return true
	}
	if strings.EqualFold(m.Phase, "PREP_DONE") && (m.State == "OK" || m.State == "" || m.State == "HEARTBEAT") {
		// PREP_DONE is emitted with state OK from the prep image.
		return m.State == "OK" || m.State == ""
	}
	return false
}

// TerminalReasonFromMarker maps a terminal marker to a reason string used by Deploy.
func TerminalReasonFromMarker(m models.SOLMarker) string {
	if strings.EqualFold(m.Phase, "DONE") && m.State == "OK" {
		return "done_ok"
	}
	return "marker_error"
}

// FormatMarkerLine renders m as a SHOAL|… protocol line for durable job_log rows.
// Empty timestamps use UTC now; nil percent is written as "-".
func FormatMarkerLine(m models.SOLMarker) string {
	ts := m.Timestamp.UTC()
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	pct := "-"
	if m.Percent != nil {
		pct = strconv.Itoa(*m.Percent)
	}
	ver := m.SchemaVer
	if ver == 0 {
		ver = SchemaVersion
	}
	return fmt.Sprintf("SHOAL|%d|%d|%s|%s|%s|%s|%s",
		ver, m.Seq, ts.Format(time.RFC3339), m.Phase, pct, m.State, m.Detail)
}
