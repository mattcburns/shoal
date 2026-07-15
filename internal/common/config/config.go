// Package config loads process environment for the Shoal app (§8.1).
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is the app configuration contract (env-backed).
type Config struct {
	HTTPAddr             string
	LogLevel             string
	TelemetryDatabaseURL string
	NetBoxURL            string
	NetBoxToken          string
	AIProvider           string
	AIModel              string
	AIVisionModel        string // optional; CompleteVision (design §6 / v2.0.4)
	OllamaURL            string
	CloudAIBaseURL       string
	CloudAIAPIKey        string
	RedfishAuthMode      string
	RedfishTLSMode       string
	RedfishCAFile        string
	BMCUsername          string
	BMCPassword          string
	ISOBaseURL           string // public HTTP prefix for Virtual Media (e.g. http://192.168.124.1:8080)
	ISOPublishDir        string // filesystem dir served on :8080 (e.g. /srv/iso); Phase 5c publish
	ISOBuildScript       string // optional path to build-marker-iso.sh
	// ISODynamic enables build+publish on Start when BuildISO is set or ISOURL empty with profile (Phase 6a).
	ISODynamic           bool
	ReconcileFailOrphans bool
	SecretsDir           string
	// Serial SSH delegate (VM-hosted lab: nest libvirt on L1).
	SerialSSHHost string
	SerialSSHUser string
	SerialSSHKey  string
	// SerialSSHSudo runs remote virsh/cat with sudo -n (default true when host set).
	SerialSSHSudo bool
	// FewShotDir is append-only learned few-shot JSONL storage (empty = learning disabled).
	FewShotDir string
	// ProfileDir is JSON profile store for Phase 5b (empty disables profile load/approve).
	ProfileDir string
}

// Load reads SHOAL_* environment variables with Phase 1-friendly defaults.
// Full lab vars (NetBox, AI, DSN) are optional until their features are used.
func Load() (Config, error) {
	c := Config{
		HTTPAddr:             envOr("SHOAL_HTTP_ADDR", ":8088"),
		LogLevel:             strings.ToLower(envOr("SHOAL_LOG_LEVEL", "info")),
		TelemetryDatabaseURL: os.Getenv("SHOAL_TELEMETRY_DATABASE_URL"),
		NetBoxURL:            os.Getenv("SHOAL_NETBOX_URL"),
		NetBoxToken:          os.Getenv("SHOAL_NETBOX_TOKEN"),
		AIProvider:           strings.ToLower(envOr("SHOAL_AI_PROVIDER", "")),
		AIModel:              os.Getenv("SHOAL_AI_MODEL"),
		AIVisionModel:        os.Getenv("SHOAL_AI_VISION_MODEL"),
		OllamaURL:            os.Getenv("SHOAL_OLLAMA_URL"),
		CloudAIBaseURL:       os.Getenv("SHOAL_CLOUD_AI_BASE_URL"),
		CloudAIAPIKey:        os.Getenv("SHOAL_CLOUD_AI_API_KEY"),
		RedfishAuthMode:      strings.ToLower(envOr("SHOAL_REDFISH_AUTH_MODE", "basic")),
		RedfishTLSMode:       strings.ToLower(envOr("SHOAL_REDFISH_TLS_MODE", "off")),
		RedfishCAFile:        os.Getenv("SHOAL_REDFISH_CA_FILE"),
		BMCUsername:          os.Getenv("SHOAL_BMC_USERNAME"),
		BMCPassword:          os.Getenv("SHOAL_BMC_PASSWORD"),
		ISOBaseURL:           os.Getenv("SHOAL_ISO_BASE_URL"),
		ISOPublishDir:        os.Getenv("SHOAL_ISO_PUBLISH_DIR"),
		ISOBuildScript:       os.Getenv("SHOAL_ISO_BUILD_SCRIPT"),
		ReconcileFailOrphans: true,
		SecretsDir:           envOr("SHOAL_SECRETS_DIR", ""),
		SerialSSHHost:        os.Getenv("SHOAL_SERIAL_SSH_HOST"),
		SerialSSHUser:        envOr("SHOAL_SERIAL_SSH_USER", "lab"),
		SerialSSHKey:         envOr("SHOAL_SERIAL_SSH_KEY", ""),
		SerialSSHSudo:        true,
		FewShotDir:           os.Getenv("SHOAL_FEWSHOT_DIR"),
		ProfileDir:           os.Getenv("SHOAL_PROFILE_DIR"),
	}

	if v := os.Getenv("SHOAL_RECONCILE_FAIL_ORPHANS"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("config: SHOAL_RECONCILE_FAIL_ORPHANS: %w", err)
		}
		c.ReconcileFailOrphans = b
	}
	if v := os.Getenv("SHOAL_ISO_DYNAMIC"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("config: SHOAL_ISO_DYNAMIC: %w", err)
		}
		c.ISODynamic = b
	}
	if v := os.Getenv("SHOAL_SERIAL_SSH_SUDO"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("config: SHOAL_SERIAL_SSH_SUDO: %w", err)
		}
		c.SerialSSHSudo = b
	}
	if c.SerialSSHKey == "" {
		// Common lab default (not required to exist).
		if home := os.Getenv("HOME"); home != "" {
			c.SerialSSHKey = home + "/.ssh/shoal_lab_vm"
		}
	}

	if err := c.validateBasics(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c Config) validateBasics() error {
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("config: invalid SHOAL_LOG_LEVEL %q", c.LogLevel)
	}
	switch c.RedfishAuthMode {
	case "basic", "session":
	default:
		return fmt.Errorf("config: invalid SHOAL_REDFISH_AUTH_MODE %q", c.RedfishAuthMode)
	}
	switch c.RedfishTLSMode {
	case "off", "insecure", "custom_ca":
	default:
		return fmt.Errorf("config: invalid SHOAL_REDFISH_TLS_MODE %q", c.RedfishTLSMode)
	}
	if c.RedfishTLSMode == "custom_ca" && c.RedfishCAFile == "" {
		return fmt.Errorf("config: SHOAL_REDFISH_CA_FILE required when TLS mode is custom_ca")
	}
	if c.AIProvider != "" {
		switch c.AIProvider {
		case "ollama", "cloud":
		default:
			return fmt.Errorf("config: invalid SHOAL_AI_PROVIDER %q", c.AIProvider)
		}
	}
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
