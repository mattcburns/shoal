// Package decode implements the AI structured-output pipeline helpers
// (fence strip → extract JSON → unmarshal).
package decode

import (
	"encoding/json"
	"fmt"
	"strings"
)

// StripCodeFences removes markdown fences such as ```json ... ``` from s.
func StripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// Drop opening fence line.
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[i+1:]
	} else {
		return strings.Trim(s, "`")
	}
	// Drop closing fence.
	if j := strings.LastIndex(s, "```"); j >= 0 {
		s = s[:j]
	}
	return strings.TrimSpace(s)
}

// ExtractJSONObject returns the first top-level JSON object substring in s.
// If s is already a bare object, it is returned trimmed.
func ExtractJSONObject(s string) (string, error) {
	s = StripCodeFences(s)
	s = strings.TrimSpace(s)
	start := strings.Index(s, "{")
	if start < 0 {
		return "", fmt.Errorf("decode: no JSON object found")
	}
	depth := 0
	inString := false
	escape := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], nil
			}
		}
	}
	return "", fmt.Errorf("decode: unclosed JSON object")
}

// DecodeJSON unmarshals the first JSON object in content into T.
func DecodeJSON[T any](content string) (T, error) {
	var zero T
	obj, err := ExtractJSONObject(content)
	if err != nil {
		return zero, err
	}
	var out T
	if err := json.Unmarshal([]byte(obj), &out); err != nil {
		return zero, fmt.Errorf("decode: unmarshal: %w", err)
	}
	return out, nil
}
