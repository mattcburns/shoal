package redfish

import (
	"context"
	"fmt"
	"io"
)

// BMC is the only Redfish surface Deploy/Observe import.
// Implementation is a hand-written Redfish HTTP client; its internal types do
// not leak outside this package.
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
	// Deploy uses this; On when already On is rewritten to ForceRestart so
	// one-time virtual media is observed.
	Power(ctx context.Context, systemID, resetType string) error
	// Reset applies the Redfish reset type as requested, with no On→ForceRestart rewrite.
	Reset(ctx context.Context, systemID, resetType string) error
	// CleanupMediaAndBoot ejects all inserted media for the system and clears boot override.
	CleanupMediaAndBoot(ctx context.Context, systemID string) error
	// ListSEL returns log entries (SEL/event logs) for the system, managers, and chassis.
	// Best-effort: missing LogServices yield an empty slice, not an error.
	ListSEL(ctx context.Context, systemID string, opts SELOptions) ([]SELEntry, error)
	// ListSensors returns thermal/power sensor samples for chassis related to systemID.
	// Best-effort: missing Thermal/Power yield empty slice or partial results.
	ListSensors(ctx context.Context, systemID string) ([]SensorSample, error)
	// ListFirmware returns UpdateService FirmwareInventory (gather-only; no flash).
	// Missing UpdateService is an empty slice, not an error (sushy).
	ListFirmware(ctx context.Context) ([]FirmwareComponent, error)
	// CaptureScreenshot attempts OEM/documented console frame capture (Dell, Supermicro first).
	// File/operator capture is preferred in lab; this path is for real BMC hardware.
	// On failure, returned Screenshot.Debug (and error text) include probe steps without secrets.
	CaptureScreenshot(ctx context.Context, systemID string, kind ScreenshotKind) (Screenshot, error)
	// OpenSOL opens a serial-over-LAN byte stream for systemID. It tries native
	// Redfish WebSocket SOL for recognized vendors (Dell, Supermicro) first, but
	// only keeps a socket that sniffs as line-oriented SOL (not HTML5 KVM). Then
	// SSH attach when eligible (Redfish SerialConsole.SSH, manager SSH connect
	// type, or Dell NetworkProtocol/OEM serial-redirection even if SerialConsole
	// is empty), then IPMI 2.0 SOL as last resort (stdlib client; not a second
	// BMC API). ctx cancellation aborts discovery/dial; this is the first BMC
	// method that owns a long-lived connection rather than one round trip, so
	// callers must cancel ctx to release it.
	OpenSOL(ctx context.Context, systemID string) (SOLStream, error)
}

// Factory constructs a BMC from config (composition root injects this).
type Factory func(cfg Config) (BMC, error)

// SOLConnectKind records which real mechanism produced a SOLStream.
type SOLConnectKind string

const (
	// SOLConnectWebSocket is a native Redfish/OEM WebSocket SOL stream.
	SOLConnectWebSocket SOLConnectKind = "websocket"
	// SOLConnectSSH is an SSH session opened because OpenSOL selected the SSH
	// backend (Redfish SerialConsole.SSH, manager SSH connect type, or Dell
	// NetworkProtocol/OEM serial-redirection).
	SOLConnectSSH SOLConnectKind = "ssh"
	// SOLConnectIPMI is IPMI 2.0 SOL (RMCP+) used as last resort inside OpenSOL.
	SOLConnectIPMI SOLConnectKind = "ipmi"
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

// SOLUnsupportedError is returned when no OpenSOL backend attached: WebSocket
// was not line-oriented SOL, SSH was ineligible or failed, and IPMI 2.0 SOL
// timed out or failed. Telnet is deferred. IPMI is a SOL payload only.
type SOLUnsupportedError struct {
	Vendor       VendorID
	ConnectTypes []string // observed enabled serial-console protocols, e.g. "IPMI", "Telnet"
	Debug        []CaptureDebugStep
}

func (e *SOLUnsupportedError) Error() string {
	return fmt.Sprintf("redfish: SOL unsupported for vendor %q (connect types: %v)\n%s",
		e.Vendor, e.ConnectTypes, formatDebug(e.Debug))
}
