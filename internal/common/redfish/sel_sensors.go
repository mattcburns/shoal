package redfish

import (
	"context"
	"fmt"
	"strings"
	"time"

	gofishredfish "github.com/stmcginnis/gofish/redfish"
)

// ListSEL collects log entries from the computer system, managers, and chassis.
//
// Empty result with nil error means sources were reachable and simply had no entries
// (or no LogServices) — normal for sushy-tools.
// Non-nil error means a hard failure: not open, system not found (when requested),
// or every discovered log service failed while reading entries (and no entries returned).
func (c *client) ListSEL(ctx context.Context, systemID string, opts SELOptions) ([]SELEntry, error) {
	_ = ctx
	api, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	max := opts.MaxEntries
	if max <= 0 {
		max = 200
	}

	seen := make(map[string]struct{})
	var out []SELEntry
	var firstReadErr error
	logServicesSeen := 0
	entryReadOK := 0
	entryReadFail := 0

	appendEntries := func(logName string, entries []*gofishredfish.LogEntry) {
		entryReadOK++
		for _, e := range entries {
			if e == nil {
				continue
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

	readLogServices := func(services []*gofishredfish.LogService) {
		for _, ls := range services {
			if ls == nil {
				continue
			}
			logServicesSeen++
			entries, err := ls.Entries()
			if err != nil {
				entryReadFail++
				if firstReadErr == nil {
					firstReadErr = err
				}
				continue
			}
			appendEntries(ls.Name, entries)
			if len(out) >= max {
				return
			}
		}
	}

	// System log services — system lookup failure is hard when systemID is set
	// or when there is not exactly one system for empty id.
	sys, sysErr := c.computerSystem(systemID)
	if sysErr != nil {
		// Still try managers/chassis (BMC may log there only), but remember error.
		if firstReadErr == nil {
			firstReadErr = sysErr
		}
	} else if sys != nil {
		if services, err := sys.LogServices(); err == nil {
			readLogServices(services)
		}
		// LogServices() missing/empty is soft.
	}
	if len(out) >= max {
		return out[:max], nil
	}

	if managers, err := api.Service.Managers(); err == nil {
		for _, m := range managers {
			if m == nil {
				continue
			}
			if services, err := m.LogServices(); err == nil {
				readLogServices(services)
				if len(out) >= max {
					return out[:max], nil
				}
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
				readLogServices(services)
				if len(out) >= max {
					return out[:max], nil
				}
			}
		}
	} else if firstReadErr == nil {
		firstReadErr = fmt.Errorf("redfish: chassis: %w", err)
	}

	if len(out) > max {
		out = out[:max]
	}

	// Hard fail only when we could not return any entries and something went wrong
	// reading entries from discovered log services, or system resolution failed with
	// no other data path producing entries.
	if len(out) == 0 {
		if entryReadFail > 0 && logServicesSeen > 0 {
			return nil, fmt.Errorf("redfish: log entry reads failed (%d services, %d read errors): %w",
				logServicesSeen, entryReadFail, firstReadErr)
		}
		// systemID explicitly requested but system missing and no entries elsewhere
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

// ListSensors collects temperature, fan, and voltage samples from chassis Thermal/Power.
//
// Chassis collection failure is an error. Missing Thermal/Power on a chassis is soft
// (skipped). Empty chassis or empty sensors with successful enumeration is OK.
func (c *client) ListSensors(ctx context.Context, systemID string) ([]SensorSample, error) {
	_ = ctx
	_ = systemID
	api, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	chassis, err := api.Service.Chassis()
	if err != nil {
		return nil, fmt.Errorf("redfish: chassis for sensors: %w", err)
	}
	var out []SensorSample
	for _, ch := range chassis {
		if ch == nil {
			continue
		}
		if thermal, err := ch.Thermal(); err == nil && thermal != nil {
			for _, t := range thermal.Temperatures {
				name := t.Name
				if name == "" {
					name = t.MemberID
				}
				if name == "" {
					name = t.ID
				}
				out = append(out, SensorSample{
					Name:            name,
					Reading:         float64(t.ReadingCelsius),
					Units:           "Cel",
					PhysicalContext: string(t.PhysicalContext),
					Kind:            "temperature",
				})
			}
			for _, f := range thermal.Fans {
				name := f.Name
				if name == "" {
					name = f.MemberID
				}
				if name == "" {
					name = f.ID
				}
				units := string(f.ReadingUnits)
				if units == "" {
					units = "RPM"
				}
				out = append(out, SensorSample{
					Name:            name,
					Reading:         float64(f.Reading),
					Units:           units,
					PhysicalContext: string(f.PhysicalContext),
					Kind:            "fan",
				})
			}
		}
		if power, err := ch.Power(); err == nil && power != nil {
			for _, v := range power.Voltages {
				name := v.Name
				if name == "" {
					name = v.MemberID
				}
				if name == "" {
					name = v.ID
				}
				out = append(out, SensorSample{
					Name:            name,
					Reading:         float64(v.ReadingVolts),
					Units:           "V",
					PhysicalContext: string(v.PhysicalContext),
					Kind:            "voltage",
				})
			}
		}
	}
	return out, nil
}
