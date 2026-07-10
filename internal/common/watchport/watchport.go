// Package watchport defines the watch registration interface Deploy consumes.
// Observe implements WatchRegistrar; Deploy must not import observe.
package watchport

import (
	"context"

	"github.com/mattcburns/shoal/internal/common/models"
)

// WatchRegistrar registers SOL (or other) watches for provisioning jobs.
// Register should reject dual ownership of a node's SOL session.
type WatchRegistrar interface {
	Register(ctx context.Context, session models.WatchSession) error
	Unregister(ctx context.Context, sessionID string) error
}
