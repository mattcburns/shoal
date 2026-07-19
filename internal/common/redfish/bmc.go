package redfish

import (
	"context"
	"fmt"
	"io"
)

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
	// CaptureScreenshot attempts OEM/documented console frame capture (Dell, Supermicro first).
	// File/operator capture is preferred in lab; this path is for real BMC hardware.
	// On failure, returned Screenshot.Debug (and error text) include probe steps without secrets.
	CaptureScreenshot(ctx context.Context, systemID string, kind ScreenshotKind) (Screenshot, error)
	// OpenSOL opens a serial-over-LAN byte stream for systemID. It tries native
	// Redfish WebSocket SOL for recognized vendors (Dell, Supermicro) first, then
	// falls back to SSH only when Redfish's own capability metadata
	// (ComputerSystem.HostSerialConsole.SSH / Manager.SerialConsole) advertises SSH
	// as enabled. Never uses raw IPMI, regardless of what a BMC advertises. If a BMC
	// advertises only IPMI/Oem/Telnet connect types with no working native WS and no
	// advertised SSH, OpenSOL returns *SOLUnsupportedError. ctx cancellation aborts
	// discovery/dial; this is the first BMC method that owns a long-lived connection
	// rather than one round trip, so callers must cancel ctx to release it.
	OpenSOL(ctx context.Context, systemID string) (SOLStream, error)
}

// Factory constructs a BMC from config (composition root injects this).
type Factory func(cfg Config) (BMC, error)

// SOLConnectKind records which real mechanism produced a SOLStream.
type SOLConnectKind string

const (
	// SOLConnectWebSocket is a native Redfish/OEM WebSocket SOL stream.
	SOLConnectWebSocket SOLConnectKind = "websocket"
	// SOLConnectSSH is an SSH session opened only because Redfish's own
	// capability metadata advertised SSH as the BMC's serial console transport.
	SOLConnectSSH SOLConnectKind = "ssh"
)

// SOLStream is an open SOL byte stream plus how it was obtained. Callers read
// raw bytes; line-splitting/marker parsing happens above this package.
type SOLStream struct {
	io.ReadCloser
	Vendor VendorID
	Kind   SOLConnectKind
	// Debug is ordered discovery/connect steps for operator diagnosis on real
	// hardware (never contains secrets — see sanitizePreview).
	Debug []CaptureDebugStep
}

// SOLUnsupportedError is returned when a BMC has no usable Redfish-discovered
// SOL path: no native WebSocket candidate worked and Redfish's own metadata
// did not advertise SSH (Telnet-only and IPMI/Oem-only BMCs land here too —
// Telnet is deferred and raw IPMI is never attempted).
type SOLUnsupportedError struct {
	Vendor       VendorID
	ConnectTypes []string // observed enabled serial-console protocols, e.g. "IPMI", "Telnet"
	Debug        []CaptureDebugStep
}

func (e *SOLUnsupportedError) Error() string {
	return fmt.Sprintf("redfish: SOL unsupported for vendor %q (connect types: %v)\n%s",
		e.Vendor, e.ConnectTypes, formatDebug(e.Debug))
}
