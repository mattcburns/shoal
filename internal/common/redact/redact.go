// Package redact strips secret-like fields from maps and slog attributes
// before they reach an LLM or a log sink.
package redact

import (
	"log/slog"
	"strings"
)

// sensitiveKeySubstrings match case-insensitively as whole key equality after
// normalizing separators, or as exact known names.
var sensitiveKeys = map[string]struct{}{
	"password":      {},
	"passwd":        {},
	"secret":        {},
	"token":         {},
	"authorization": {},
	"api_key":       {},
	"apikey":        {},
	"bmc_password":  {},
	"bmcpassword":   {},
	"access_token":  {},
	"refresh_token": {},
	"private_key":   {},
	"client_secret": {},
}

// IsSensitiveKey reports whether key is a secret-bearing field name.
func IsSensitiveKey(key string) bool {
	n := normalizeKey(key)
	if _, ok := sensitiveKeys[n]; ok {
		return true
	}
	// Suffix / compound forms: foo_password, bmc-token, API-KEY
	for sk := range sensitiveKeys {
		if strings.HasSuffix(n, "_"+sk) || strings.HasSuffix(n, sk) && len(n) > len(sk) {
			// Require boundary: ends with _sk or equals handled above.
			if strings.HasSuffix(n, "_"+sk) {
				return true
			}
		}
	}
	// Common compound patterns
	if strings.Contains(n, "password") || strings.Contains(n, "passwd") {
		return true
	}
	if strings.Contains(n, "secret") || strings.Contains(n, "api_key") {
		return true
	}
	if n == "authorization" || strings.HasSuffix(n, "_token") || strings.HasSuffix(n, "token") && strings.Contains(n, "token") {
		// avoid redacting "tokenize" etc. — only exact token / *_token
		if n == "token" || strings.HasSuffix(n, "_token") {
			return true
		}
	}
	return false
}

func normalizeKey(key string) string {
	k := strings.ToLower(strings.TrimSpace(key))
	k = strings.ReplaceAll(k, "-", "_")
	k = strings.ReplaceAll(k, " ", "_")
	return k
}

// Map returns a deep copy of m with sensitive keys redacted recursively.
// Non-map/slice values that are not nested structures are copied as-is
// except when the key is sensitive (replaced with "[REDACTED]").
func Map(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if IsSensitiveKey(k) {
			out[k] = "[REDACTED]"
			continue
		}
		out[k] = redactValue(v)
	}
	return out
}

func redactValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return Map(t)
	case map[string]string:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if IsSensitiveKey(k) {
				out[k] = "[REDACTED]"
			} else {
				out[k] = val
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, el := range t {
			out[i] = redactValue(el)
		}
		return out
	default:
		return v
	}
}

// ContainsSensitiveKey reports whether m (recursively) still has an unredacted
// sensitive key whose value is not the placeholder "[REDACTED]".
func ContainsSensitiveKey(m map[string]any) bool {
	if m == nil {
		return false
	}
	for k, v := range m {
		if IsSensitiveKey(k) {
			if s, ok := v.(string); !ok || s != "[REDACTED]" {
				return true
			}
		}
		switch t := v.(type) {
		case map[string]any:
			if ContainsSensitiveKey(t) {
				return true
			}
		case []any:
			for _, el := range t {
				if nested, ok := el.(map[string]any); ok && ContainsSensitiveKey(nested) {
					return true
				}
			}
		}
	}
	return false
}

// ReplaceAttr is a slog.HandlerOptions.ReplaceAttr that redacts sensitive attr keys.
func ReplaceAttr(_ []string, a slog.Attr) slog.Attr {
	if IsSensitiveKey(a.Key) {
		return slog.String(a.Key, "[REDACTED]")
	}
	// Nested groups: walk Group values
	if a.Value.Kind() == slog.KindGroup {
		attrs := a.Value.Group()
		redacted := make([]any, 0, len(attrs)*2)
		for _, ga := range attrs {
			ra := ReplaceAttr(nil, ga)
			redacted = append(redacted, ra.Key, ra.Value.Any())
		}
		return slog.Group(a.Key, redacted...)
	}
	return a
}
