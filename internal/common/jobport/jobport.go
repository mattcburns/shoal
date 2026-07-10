// Package jobport defines the progress interface Observe consumes.
// Deploy implements JobProgress; Observe must not import deploy.
package jobport

import (
	"context"

	"github.com/mattcburns/shoal/internal/common/models"
)

// JobProgress is how Observe proposes progress without owning lifecycle transitions.
// Implementations write progress via JobStore and notify the Orchestrator for terminal events.
// ApplyMarker MUST NOT run Redfish cleanup inline.
type JobProgress interface {
	ApplyMarker(ctx context.Context, jobID string, m models.SOLMarker) error
	ReportStall(ctx context.Context, jobID string, reason string) error
	ReportTransportError(ctx context.Context, jobID string, err error) error
}
