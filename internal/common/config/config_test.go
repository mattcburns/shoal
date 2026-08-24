package config_test

import (
	"testing"
	"time"

	"github.com/mattcburns/shoal/internal/common/config"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("SHOAL_HTTP_ADDR", "")
	t.Setenv("SHOAL_LOG_LEVEL", "")
	t.Setenv("SHOAL_REDFISH_AUTH_MODE", "")
	t.Setenv("SHOAL_REDFISH_TLS_MODE", "")
	t.Setenv("SHOAL_TELEMETRY_DATABASE_URL", "")
	t.Setenv("SHOAL_AI_PROVIDER", "")
	// Clear potentially set vars that Load reads
	for _, k := range []string{
		"SHOAL_HTTP_ADDR", "SHOAL_LOG_LEVEL", "SHOAL_TELEMETRY_DATABASE_URL",
		"SHOAL_NETBOX_URL", "SHOAL_NETBOX_TOKEN", "SHOAL_AI_PROVIDER", "SHOAL_AI_MODEL",
		"SHOAL_OLLAMA_URL", "SHOAL_CLOUD_AI_BASE_URL", "SHOAL_CLOUD_AI_API_KEY",
		"SHOAL_REDFISH_AUTH_MODE", "SHOAL_REDFISH_TLS_MODE", "SHOAL_REDFISH_CA_FILE",
		"SHOAL_BMC_USERNAME", "SHOAL_BMC_PASSWORD", "SHOAL_ISO_BASE_URL",
		"SHOAL_RECONCILE_FAIL_ORPHANS", "SHOAL_SECRETS_DIR",
		"SHOAL_POLL_IDLE_INTERVAL", "SHOAL_POLL_WATCH_INTERVAL",
	} {
		t.Setenv(k, "")
	}

	c, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPAddr != ":8088" {
		t.Fatalf("HTTPAddr=%q", c.HTTPAddr)
	}
	if c.LogLevel != "info" {
		t.Fatalf("LogLevel=%q", c.LogLevel)
	}
	if c.RedfishAuthMode != "basic" {
		t.Fatalf("RedfishAuthMode=%q", c.RedfishAuthMode)
	}
	if !c.ReconcileFailOrphans {
		t.Fatal("ReconcileFailOrphans default true")
	}
	if c.PollIdleInterval != 5*time.Minute {
		t.Fatalf("PollIdleInterval=%s", c.PollIdleInterval)
	}
	if c.PollWatchInterval != 30*time.Second {
		t.Fatalf("PollWatchInterval=%s", c.PollWatchInterval)
	}
}

func TestLoadCustom(t *testing.T) {
	t.Setenv("SHOAL_HTTP_ADDR", ":9090")
	t.Setenv("SHOAL_LOG_LEVEL", "debug")
	t.Setenv("SHOAL_TELEMETRY_DATABASE_URL", "postgres://shoal@localhost:5433/shoal_telemetry")
	t.Setenv("SHOAL_AI_PROVIDER", "ollama")
	t.Setenv("SHOAL_RECONCILE_FAIL_ORPHANS", "false")
	t.Setenv("SHOAL_SERIAL_SSH_HOST", "192.168.122.100")
	t.Setenv("SHOAL_SERIAL_SSH_USER", "lab")
	t.Setenv("SHOAL_SERIAL_SSH_KEY", "/tmp/key")
	t.Setenv("SHOAL_POLL_IDLE_INTERVAL", "2m")
	t.Setenv("SHOAL_POLL_WATCH_INTERVAL", "10s")
	c, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPAddr != ":9090" {
		t.Fatalf("HTTPAddr=%q", c.HTTPAddr)
	}
	if c.LogLevel != "debug" {
		t.Fatalf("LogLevel=%q", c.LogLevel)
	}
	if c.TelemetryDatabaseURL == "" {
		t.Fatal("expected DSN")
	}
	if c.ReconcileFailOrphans {
		t.Fatal("expected false")
	}
	if c.SerialSSHHost != "192.168.122.100" || c.SerialSSHKey != "/tmp/key" {
		t.Fatalf("serial ssh: host=%q key=%q", c.SerialSSHHost, c.SerialSSHKey)
	}
	if c.PollIdleInterval != 2*time.Minute || c.PollWatchInterval != 10*time.Second {
		t.Fatalf("poll intervals idle=%s watch=%s", c.PollIdleInterval, c.PollWatchInterval)
	}
}

func TestLoadInvalidPollInterval(t *testing.T) {
	t.Setenv("SHOAL_POLL_IDLE_INTERVAL", "nope")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected error")
	}
	t.Setenv("SHOAL_POLL_IDLE_INTERVAL", "0s")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected error for zero")
	}
}

func TestLoadInvalidLogLevel(t *testing.T) {
	t.Setenv("SHOAL_LOG_LEVEL", "verbose")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected error")
	}
}
