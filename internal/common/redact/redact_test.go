package redact_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/mattcburns/shoal/internal/common/redact"
)

func TestMapRedactsSensitiveKeys(t *testing.T) {
	in := map[string]any{
		"username":     "admin",
		"password":     "s3cret",
		"bmc_password": "bmc-pass",
		"api_key":      "key-123",
		"nested": map[string]any{
			"token": "tok",
			"host":  "bmc.example",
		},
		"items": []any{
			map[string]any{"passwd": "x", "ok": true},
		},
	}
	out := redact.Map(in)
	if out["password"] != "[REDACTED]" {
		t.Fatalf("password: got %v", out["password"])
	}
	if out["bmc_password"] != "[REDACTED]" {
		t.Fatalf("bmc_password: got %v", out["bmc_password"])
	}
	if out["api_key"] != "[REDACTED]" {
		t.Fatalf("api_key: got %v", out["api_key"])
	}
	if out["username"] != "admin" {
		t.Fatalf("username should remain: %v", out["username"])
	}
	nested := out["nested"].(map[string]any)
	if nested["token"] != "[REDACTED]" {
		t.Fatalf("nested token: %v", nested["token"])
	}
	if nested["host"] != "bmc.example" {
		t.Fatalf("nested host: %v", nested["host"])
	}
	// Original must not be mutated
	if in["password"] != "s3cret" {
		t.Fatal("Map must not mutate input")
	}
	if redact.ContainsSensitiveKey(out) {
		t.Fatal("redacted map should not report sensitive unredacted keys")
	}
	if !redact.ContainsSensitiveKey(in) {
		t.Fatal("input still has secrets")
	}
}

func TestSlogReplaceAttrRedactsSecrets(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: redact.ReplaceAttr,
	})
	log := slog.New(h)
	log.Info("attempt",
		"bmc_password", "super-secret-password",
		"user", "admin",
		"token", "xyz",
	)
	line := buf.String()
	if strings.Contains(line, "super-secret-password") {
		t.Fatalf("password leaked into log: %s", line)
	}
	if strings.Contains(line, `"xyz"`) && strings.Contains(line, "token") {
		// token value must be redacted
		var m map[string]any
		if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		if m["token"] != "[REDACTED]" {
			t.Fatalf("token not redacted: %v", m["token"])
		}
		if m["bmc_password"] != "[REDACTED]" {
			t.Fatalf("bmc_password not redacted: %v", m["bmc_password"])
		}
		if m["user"] != "admin" {
			t.Fatalf("user: %v", m["user"])
		}
	}
}

func TestIsSensitiveKey(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		{"password", true},
		{"Password", true},
		{"BMC_PASSWORD", true},
		{"api-key", true},
		{"authorization", true},
		{"serial", false},
		{"model", false},
		{"username", false},
	}
	for _, tc := range cases {
		if got := redact.IsSensitiveKey(tc.key); got != tc.want {
			t.Errorf("IsSensitiveKey(%q)=%v want %v", tc.key, got, tc.want)
		}
	}
}
