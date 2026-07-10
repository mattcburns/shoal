package config_test

import (
	"testing"

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
}

func TestLoadInvalidLogLevel(t *testing.T) {
	t.Setenv("SHOAL_LOG_LEVEL", "verbose")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected error")
	}
}
