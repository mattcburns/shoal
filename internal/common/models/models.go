// Package models holds shared data structs only (no business logic).
package models

import "time"

// NormalizedAsset is identity + access metadata only.
// BMC username/password are NEVER stored here and NEVER sent to an LLM.
type NormalizedAsset struct {
	Serial        string `json:"serial"`
	Model         string `json:"model"`
	Vendor        string `json:"vendor"`
	BMCIP         string `json:"bmc_ip"`
	CredentialRef string `json:"credential_ref"`
}

// FieldConfidence records how a normalized field was sourced and how sure we are.
type FieldConfidence struct {
	Field      string  `json:"field"`
	Confidence float64 `json:"confidence"` // 0.0–1.0
	Source     string  `json:"source"`     // "deterministic" | "ai"
	Evidence   string  `json:"evidence,omitempty"`
}

// NormalizationResult is the output of hybrid (or AI-only) asset normalization.
type NormalizationResult struct {
	Asset       NormalizedAsset   `json:"asset"`
	Confidences []FieldConfidence `json:"confidences"`
	NeedsReview bool              `json:"needs_review"`
}

// NormalizedEvent is a telemetry/event record correlated to a device.
type NormalizedEvent struct {
	DeviceID  string    `json:"device_id"` // required for telemetry.events.device_id correlation
	EventType string    `json:"event_type"`
	Severity  string    `json:"severity"`
	Component string    `json:"component"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
	// Raw must be redacted before any LLM call; may be empty in API responses.
	Raw map[string]any `json:"raw,omitempty"`
}

// SOLMarker is the parsed SHOAL|... protocol record (shared DTO).
type SOLMarker struct {
	SchemaVer int       `json:"schema_ver"`
	Seq       int       `json:"seq"`
	Timestamp time.Time `json:"timestamp"`
	Phase     string    `json:"phase"`
	Percent   *int      `json:"percent,omitempty"` // nil when "-"
	State     string    `json:"state"`             // OK | WARN | ERROR | HEARTBEAT
	Detail    string    `json:"detail,omitempty"`
}

// LifecycleState is the device/job lifecycle enum written only by Deploy Orchestrator.
type LifecycleState string

const (
	StateDiscovered   LifecycleState = "discovered"
	StateReady        LifecycleState = "ready"
	StateProvisioning LifecycleState = "provisioning"
	StateProvisioned  LifecycleState = "provisioned"
	StateFailed       LifecycleState = "failed"
)

// ProvisioningJob is durable job state (JobStore / Postgres jobs table).
type ProvisioningJob struct {
	ID            string         `json:"id"`
	DeviceID      string         `json:"device_id"`
	ProfileRef    string         `json:"profile_ref"`
	State         LifecycleState `json:"state"`
	Attempt       int            `json:"attempt"`
	Phase         string         `json:"phase,omitempty"`
	Percent       *int           `json:"percent,omitempty"`
	LastMarkerSeq int            `json:"last_marker_seq"`
	StartedAt     *time.Time     `json:"started_at,omitempty"`
	UpdatedAt     *time.Time     `json:"updated_at,omitempty"`
	Error         string         `json:"error,omitempty"`
	SOLSessionID  string         `json:"sol_session_id,omitempty"`
	ISOURL        string         `json:"iso_url,omitempty"`
	BMCEndpoint   string         `json:"bmc_endpoint,omitempty"`
}

// RawAssetInput is Discover ingest (API/CLI).
type RawAssetInput struct {
	Kind string `json:"kind"` // "redfish_json" | "csv" | "photo"

	// Exactly one payload depending on Kind:
	RedfishJSON map[string]any    `json:"redfish_json,omitempty"`
	CSVRow      map[string]string `json:"csv_row,omitempty"`
	// PhotoBase64 is JPEG/PNG; max decoded size 4 MiB.
	PhotoBase64 string `json:"photo_base64,omitempty"`

	// Optional operator-supplied BMC access for first ingest (stored in secrets,
	// never copied into NormalizedAsset password fields).
	BMCUsername string `json:"bmc_username,omitempty"`
	BMCPassword string `json:"bmc_password,omitempty"` // accepted once; not logged; not returned
	BMCIP       string `json:"bmc_ip,omitempty"`
}

// RawEventInput is Observe → Core event reconciliation input.
type RawEventInput struct {
	DeviceID  string         `json:"device_id"`
	Source    string         `json:"source"` // "sel" | "sensor" | "sol" | "ocr"
	Timestamp time.Time      `json:"timestamp"`
	Message   string         `json:"message"`
	Raw       map[string]any `json:"raw,omitempty"` // redact before LLM
}

// ProfileRequirements is non-secret operator intent for profile generation.
type ProfileRequirements struct {
	OSFamily      string            `json:"os_family"` // e.g. "ubuntu"
	OSVersion     string            `json:"os_version,omitempty"`
	Hostname      string            `json:"hostname,omitempty"`
	Extra         map[string]string `json:"extra,omitempty"` // no password keys allowed
	AllowDestruct bool              `json:"allow_destruct"`  // human gate
}

// ProvisioningProfile is a schema-validated AI or operator profile.
type ProvisioningProfile struct {
	Ref              string   `json:"ref"`
	ISOBase          string   `json:"iso_base"`
	EmbeddedPayload  string   `json:"embedded_payload,omitempty"`
	PostInstallSteps []string `json:"post_install_steps,omitempty"`
	// DestructSteps require AllowDestruct + explicit operator approval before Deploy runs them.
	DestructSteps []string `json:"destruct_steps,omitempty"`
	NeedsApproval bool     `json:"needs_approval"`
}

// DeviceStatus is the Observe aggregate view.
type DeviceStatus struct {
	DeviceID       string         `json:"device_id"`
	LifecycleState LifecycleState `json:"lifecycle_state"`
	PowerState     string         `json:"power_state,omitempty"`
	LastEvent      string         `json:"last_event,omitempty"`
	ActiveJobID    string         `json:"active_job_id,omitempty"`
	Phase          string         `json:"phase,omitempty"`
	Percent        *int           `json:"percent,omitempty"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

// WatchSession is Observe registration during a job.
type WatchSession struct {
	ID           string        `json:"id"`
	JobID        string        `json:"job_id"`
	DeviceID     string        `json:"device_id"`
	Transport    string        `json:"transport"` // "libvirt" | "redfish_sol" | "ipmi_sol"
	Target       string        `json:"target"`    // console path or BMC URI
	StartedAt    time.Time     `json:"started_at"`
	StallTimeout time.Duration `json:"stall_timeout"` // e.g. 90s
}

// DeviceIdentity is NetBox-facing identity fields.
type DeviceIdentity struct {
	ID             string         `json:"id,omitempty"`
	Name           string         `json:"name,omitempty"`
	Serial         string         `json:"serial"`
	LifecycleState LifecycleState `json:"lifecycle_state"`
	CredentialRef  string         `json:"credential_ref"`
	BMCIP          string         `json:"bmc_ip"`
}

// StartJobRequest carries Phase 2 binding fields; NetBox-only resolution is post-Phase 3 optional.
type StartJobRequest struct {
	DeviceID      string `json:"device_id"`
	ProfileRef    string `json:"profile_ref,omitempty"`
	ISOURL        string `json:"iso_url"`
	BMCEndpoint   string `json:"bmc_endpoint"`           // Redfish base URL
	BMCUsername   string `json:"bmc_username,omitempty"` // stored to secrets; not logged
	BMCPassword   string `json:"bmc_password,omitempty"` // stored to secrets; never returned
	SerialTarget  string `json:"serial_target"`          // libvirt domain or SOL target
	SystemID      string `json:"system_id,omitempty"`
	CredentialRef string `json:"credential_ref,omitempty"` // alt: pre-seeded secret (skip user/pass)
	// StallTimeout is the SOL silence window before ReportStall (0 = Orchestrator default).
	StallTimeout time.Duration `json:"stall_timeout,omitempty"`
}

// CancelJobRequest cancels an in-flight provisioning job.
type CancelJobRequest struct {
	JobID string `json:"job_id"`
}
