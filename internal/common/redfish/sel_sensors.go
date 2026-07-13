package redfish

import (
	"context"
	"strings"
	"time"

	gofishredfish "github.com/stmcginnis/gofish/redfish"
)

// ListSEL collects log entries from the computer system, managers, and chassis.
// Missing log services are skipped; returns empty slice when none are present.
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

	appendEntries := func(logName string, entries []*gofishredfish.LogEntry) {
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
			if len(out) >= max {
				return
			}
		}
	}

	// System log services
	if sys, err := c.computerSystem(systemID); err == nil && sys != nil {
		if services, err := sys.LogServices(); err == nil {
			for _, ls := range services {
				if ls == nil {
					continue
				}
				entries, err := ls.Entries()
				if err != nil {
					continue
				}
				appendEntries(ls.Name, entries)
				if len(out) >= max {
					return out, nil
				}
			}
		}
	}

	// Managers
	if managers, err := api.Service.Managers(); err == nil {
		for _, m := range managers {
			if m == nil {
				continue
			}
			services, err := m.LogServices()
			if err != nil {
				continue
			}
			for _, ls := range services {
				if ls == nil {
					continue
				}
				entries, err := ls.Entries()
				if err != nil {
					continue
				}
				appendEntries(ls.Name, entries)
				if len(out) >= max {
					return out, nil
				}
			}
		}
	}

	// Chassis
	if chassis, err := api.Service.Chassis(); err == nil {
		for _, ch := range chassis {
			if ch == nil {
				continue
			}
			services, err := ch.LogServices()
			if err != nil {
				continue
			}
			for _, ls := range services {
				if ls == nil {
					continue
				}
				entries, err := ls.Entries()
				if err != nil {
					continue
				}
				appendEntries(ls.Name, entries)
				if len(out) >= max {
					return out, nil
				}
			}
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
	// Common Redfish formats
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
// Best-effort: skips chassis/subresources that are missing or error.
func (c *client) ListSensors(ctx context.Context, systemID string) ([]SensorSample, error) {
	_ = ctx
	_ = systemID // chassis list is global for multi-node sushy; filter later if needed
	api, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	chassis, err := api.Service.Chassis()
	if err != nil {
		// No chassis collection → empty sensors, not hard failure for Observe poll.
		return nil, nil
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
