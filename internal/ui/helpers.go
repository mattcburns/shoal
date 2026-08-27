package ui

import (
	"net/http"
	"strconv"
	"strings"
)

// maxListLimit mirrors internal/api/server.go's constant of the same name:
// the hard cap on any ?limit= query param so a UI page can't force an
// unbounded scan.
const maxListLimit = 200

// parseLimit mirrors internal/api/devices.go's parseLimit exactly: parses
// the ?limit= query param, falling back to def when absent or non-positive,
// and clamping to max.
func parseLimit(r *http.Request, def, max int) int {
	limit := def
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > max {
		limit = max
	}
	return limit
}

// isNotConfiguredErr mirrors internal/api/errors.go's helper of the same
// name: reports whether err represents an optional dependency that simply
// isn't wired up (e.g. Telemetry unset) rather than a configured dependency
// that failed at call time.
func isNotConfiguredErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "not configured")
}
