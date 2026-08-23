package redfish

import (
	"context"
	"fmt"
	"strings"
	"time"

	gofishcommon "github.com/stmcginnis/gofish/common"
	gofishredfish "github.com/stmcginnis/gofish/redfish"
)

const (
	logRankVerbose = 0 // Dell Lclog / FaultList: skip unless nothing else yielded entries
	logRankNormal  = 1
	logRankSEL     = 2
)

// logServiceRank prefers IPMI SEL over vendor dumps (iDRAC LC log can be thousands
// of entries; gofish Entries() GETs every member and blows a poll deadline).
func logServiceRank(ls *gofishredfish.LogService) int {
	if ls == nil {
		return logRankVerbose
	}
	if ls.LogEntryType == gofishredfish.SELLogEntryTypes {
		return logRankSEL
	}
	blob := strings.ToLower(ls.Name + " " + ls.ID + " " + ls.ODataID)
	switch {
	case strings.Contains(blob, "lclog"),
		strings.Contains(blob, "lifecycle"),
		strings.Contains(blob, "faultlist"),
		strings.Contains(blob, "fault-list"):
		return logRankVerbose
	case strings.Contains(blob, "sel"),
		strings.Contains(blob, "systemevent"),
		strings.Contains(blob, "system_event"):
		return logRankSEL
	default:
		return logRankNormal
	}
}

// ListSEL collects log entries from the computer system, managers, and chassis.
//
// Empty result with nil error means sources were reachable and simply had no entries
// (or no LogServices) — normal for sushy-tools.
// Non-nil error means a hard failure: not open, system not found (when requested),
// or every discovered log service failed while reading entries (and no entries returned).
//
// Reads IPMI SEL first. Vendor lifecycle / fault dumps (iDRAC Lclog, FaultList)
// are skipped unless no other service returned entries. Entry collections are
// fetched with $top=MaxEntries so a huge log cannot stall Observe poll.
func (c *client) ListSEL(ctx context.Context, systemID string, opts SELOptions) ([]SELEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	api, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	max := opts.MaxEntries
	if max <= 0 {
		max = 200
	}

	var discovered []*gofishredfish.LogService
	var firstReadErr error
	var sysErr error

	sys, sysErr := c.computerSystem(systemID)
	if sysErr != nil {
		firstReadErr = sysErr
	} else if sys != nil {
		if services, err := sys.LogServices(); err == nil {
			discovered = append(discovered, services...)
		}
	}

	if managers, err := api.Service.Managers(); err == nil {
		for _, m := range managers {
			if m == nil {
				continue
			}
			if services, err := m.LogServices(); err == nil {
				discovered = append(discovered, services...)
			}
		}
	} else if firstReadErr == nil {
		firstReadErr = fmt.Errorf("redfish: managers: %w", err)
	}

	if chassis, err := api.Service.Chassis(); err == nil {
		for _, ch := range chassis {
			if ch == nil {
				continue
			}
			if services, err := ch.LogServices(); err == nil {
				discovered = append(discovered, services...)
			}
		}
	} else if firstReadErr == nil {
		firstReadErr = fmt.Errorf("redfish: chassis: %w", err)
	}

	seen := make(map[string]struct{})
	var out []SELEntry
	logServicesSeen := 0
	entryReadOK := 0
	entryReadFail := 0

	appendEntries := func(logName string, entries []*gofishredfish.LogEntry) {
		entryReadOK++
		for _, e := range entries {
			if e == nil {
				continue
			}
			if len(out) >= max {
				return
			}
			se := mapLogEntry(logName, e)
			key := se.ODataID
			if key == "" {
				key = se.ID + "|" + se.Message + "|" + se.Created.String()
			}
			if _, ok := seen[key]; ok {
				continue
			}
			if !opts.Since.IsZero() && !se.Created.IsZero() && se.Created.Before(opts.Since) {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, se)
		}
	}

	readOne := func(ls *gofishredfish.LogService, allowUnfiltered bool) {
		if ls == nil {
			return
		}
		if err := ctx.Err(); err != nil {
			if firstReadErr == nil {
				firstReadErr = err
			}
			return
		}
		logServicesSeen++
		remain := max - len(out)
		if remain <= 0 {
			return
		}
		entries, err := ls.FilteredEntries(gofishcommon.WithTop(remain))
		if err != nil && allowUnfiltered {
			entries, err = ls.Entries()
		}
		if err != nil {
			entryReadFail++
			if firstReadErr == nil {
				firstReadErr = err
			}
			return
		}
		appendEntries(ls.Name, entries)
	}

	// Pass 0: SEL. Pass 1: other non-verbose. Pass 2: LC/fault dumps only if still empty.
	for pass := logRankSEL; pass >= logRankVerbose; pass-- {
		if len(out) >= max {
			break
		}
		if pass == logRankVerbose && len(out) > 0 {
			break
		}
		if err := ctx.Err(); err != nil {
			if firstReadErr == nil {
				firstReadErr = err
			}
			break
		}
		for _, ls := range discovered {
			if logServiceRank(ls) != pass {
				continue
			}
			readOne(ls, pass >= logRankNormal)
			if len(out) >= max {
				break
			}
		}
	}

	if len(out) > max {
		out = out[:max]
	}

	if len(out) == 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entryReadFail > 0 && logServicesSeen > 0 {
			return nil, fmt.Errorf("redfish: log entry reads failed (%d services, %d read errors): %w",
				logServicesSeen, entryReadFail, firstReadErr)
		}
		if systemID != "" && sysErr != nil {
			return nil, fmt.Errorf("redfish: list SEL: %w", sysErr)
		}
	}
	return out, nil
}

func mapLogEntry(logName string, e *gofishredfish.LogEntry) SELEntry {
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = strings.TrimSpace(e.Description)
	}
	if msg == "" {
		msg = e.MessageID
	}
	if msg == "" {
		msg = e.ID
	}
	created := parseRedfishTime(e.Created)
	if created.IsZero() {
		created = parseRedfishTime(e.EventTimestamp)
	}
	return SELEntry{
		ID:         e.ID,
		Message:    msg,
		Severity:   string(e.Severity),
		EntryType:  string(e.EntryType),
		Created:    created,
		SensorType: string(e.SensorType),
		SensorNum:  e.SensorNumber,
		ODataID:    e.ODataID,
		LogService: logName,
	}
}

func parseRedfishTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05-07:00",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// ListSensors collects chassis samples: Redfish Sensor resources first (power,
// current, usage, temps), then Thermal temps/fans. Power.Voltages is only used
// when the Sensors collection is empty — iDRAC Power.Voltages is mostly discrete
// power-good rails that drown Observe/NetBox in 0V rows while the host is off.
//
// Chassis collection failure is an error. Missing Thermal/Power/Sensors on a
// chassis is soft. Empty chassis or empty sensors with successful enumeration is OK.
func (c *client) ListSensors(ctx context.Context, systemID string) ([]SensorSample, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	api, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	chassis, err := api.Service.Chassis()
	if len(chassis) == 0 {
		if err != nil {
			return nil, fmt.Errorf("redfish: chassis for sensors: %w", err)
		}
		return nil, nil
	}
	hostOff := false
	if sys, err := c.computerSystem(systemID); err == nil && sys != nil {
		hostOff = strings.EqualFold(string(sys.PowerState), "Off")
	}

	var out []SensorSample
	seen := make(map[string]struct{})
	add := func(s SensorSample) {
		name := strings.TrimSpace(s.Name)
		if name == "" {
			return
		}
		s.Name = name
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}
	for _, ch := range chassis {
		if err := ctx.Err(); err != nil {
			if len(out) > 0 {
				return out, nil
			}
			return nil, err
		}
		if ch == nil {
			continue
		}
		fromSensors := false
		if list, err := ch.Sensors(); err == nil {
			for _, s := range list {
				if s == nil {
					continue
				}
				fromSensors = true
				kind := strings.ToLower(string(s.ReadingType))
				if kind == "" {
					kind = "sensor"
				}
				sample := SensorSample{
					Name:            uniqueSensorName(seen, firstNonEmpty(s.Name, s.ID), s.ID),
					Reading:         float64(s.Reading),
					HasReading:      sensorReporting(s),
					Units:           s.ReadingUnits,
					PhysicalContext: string(s.PhysicalContext),
					Status:          strings.TrimSpace(string(s.Status.State)),
					Kind:            kind,
				}
				if !sample.HasReading {
					sample.Note = sensorUnavailableNote(s, hostOff)
				}
				add(sample)
			}
		}
		if fromSensors {
			// Sensors collection already has temps; Thermal 0°C rows would
			// look like readings when Redfish actually omitted them.
			continue
		}
		if thermal, err := ch.Thermal(); err == nil && thermal != nil {
			for _, t := range thermal.Temperatures {
				add(SensorSample{
					Name:            firstNonEmpty(t.Name, t.MemberID, t.ID),
					Reading:         float64(t.ReadingCelsius),
					HasReading:      true,
					Units:           "Cel",
					PhysicalContext: string(t.PhysicalContext),
					Kind:            "temperature",
				})
			}
			for _, f := range thermal.Fans {
				units := string(f.ReadingUnits)
				if units == "" {
					units = "RPM"
				}
				add(SensorSample{
					Name:            firstNonEmpty(f.Name, f.MemberID, f.ID),
					Reading:         float64(f.Reading),
					HasReading:      true,
					Units:           units,
					PhysicalContext: string(f.PhysicalContext),
					Kind:            "fan",
				})
			}
		}
		if power, err := ch.Power(); err == nil && power != nil {
			for _, v := range power.Voltages {
				name := firstNonEmpty(v.Name, v.MemberID, v.ID)
				if discretePowerGood(name) {
					continue
				}
				add(SensorSample{
					Name:            name,
					Reading:         float64(v.ReadingVolts),
					HasReading:      true,
					Units:           "V",
					PhysicalContext: string(v.PhysicalContext),
					Kind:            "voltage",
				})
			}
		}
	}
	return out, nil
}

func sensorReporting(s *gofishredfish.Sensor) bool {
	if s == nil {
		return false
	}
	return strings.TrimSpace(string(s.Status.State)) != ""
}

func sensorUnavailableNote(s *gofishredfish.Sensor, hostOff bool) string {
	st := ""
	if s != nil {
		st = strings.TrimSpace(string(s.Status.State))
	}
	if st != "" && !strings.EqualFold(st, "enabled") {
		return "BMC sensor state: " + st
	}
	if hostOff {
		return "No reading while host is off"
	}
	return "BMC did not return a reading"
}

func uniqueSensorName(seen map[string]struct{}, name, id string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return strings.TrimSpace(id)
	}
	if _, ok := seen[strings.ToLower(name)]; !ok {
		return name
	}
	id = strings.TrimSpace(id)
	if id == "" || strings.EqualFold(id, name) {
		return name
	}
	return name + " (" + id + ")"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// discretePowerGood is an iDRAC Power.Voltages name that is a digital
// power-good / fault bit, not an analog rail reading.
func discretePowerGood(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, " pg") || strings.HasSuffix(n, "pg") ||
		strings.Contains(n, "vshort") || strings.Contains(n, "pfault")
}
