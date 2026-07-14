package redfish

import "context"

// BMC is the only Redfish surface Deploy/Observe import.
// Implementation uses gofish under the hood; types do not leak.
type BMC interface {
	Open(ctx context.Context) error
	Close(ctx context.Context) error
	ServiceRoot(ctx context.Context) (ServiceRoot, error)
	// ListSystems returns computer systems (systemID filter optional empty = all).
	ListSystems(ctx context.Context) ([]SystemInfo, error)
	GetSystem(ctx context.Context, systemID string) (SystemInfo, error)
	GetBoot(ctx context.Context, systemID string) (BootInfo, error)
	SetBootOverrideOnceCD(ctx context.Context, systemID string) error
	ClearBootOverride(ctx context.Context, systemID string) error
	// ListVirtualMedia lists media under the given system (preferred) or all managers if systemID empty.
	ListVirtualMedia(ctx context.Context, systemID string) ([]VirtualMedia, error)
	InsertVirtualMedia(ctx context.Context, mediaURI, imageURL string) error
	EjectVirtualMedia(ctx context.Context, mediaURI string) error
	// Power resetType: On | ForceOff | ForceRestart | GracefulRestart | ...
	Power(ctx context.Context, systemID, resetType string) error
	// CleanupMediaAndBoot ejects all inserted media for the system and clears boot override.
	CleanupMediaAndBoot(ctx context.Context, systemID string) error
	// ListSEL returns log entries (SEL/event logs) for the system, managers, and chassis.
	// Best-effort: missing LogServices yield an empty slice, not an error.
	ListSEL(ctx context.Context, systemID string, opts SELOptions) ([]SELEntry, error)
	// ListSensors returns thermal/power sensor samples for chassis related to systemID.
	// Best-effort: missing Thermal/Power yield empty slice or partial results.
	ListSensors(ctx context.Context, systemID string) ([]SensorSample, error)
}

// Factory constructs a BMC from config (composition root injects this).
type Factory func(cfg Config) (BMC, error)
