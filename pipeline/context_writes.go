package pipeline

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ParseDeclaredKeys splits a comma-separated attr value into trimmed keys.
func ParseDeclaredKeys(attr string) []string {
	if attr == "" {
		return nil
	}
	var keys []string
	for _, key := range strings.Split(attr, ",") {
		key = strings.TrimSpace(key)
		if key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

// ExtractDeclaredWrites parses rawJSON as a top-level JSON object and extracts
// each declared key as a context update value.
//
// String fields are written as plain strings. Non-string fields are written as
// compact JSON text (for example arrays, objects, numbers, booleans, null).
func ExtractDeclaredWrites(writes []string, rawJSON string) (updates map[string]string, extras []string, err error) {
	writes = normalizeDeclaredKeys(writes)
	if len(writes) == 0 {
		return nil, nil, nil
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rawJSON), &obj); err != nil {
		return nil, nil, fmt.Errorf("invalid JSON object: %w", err)
	}
	if obj == nil {
		obj = make(map[string]json.RawMessage)
	}

	updates = make(map[string]string, len(writes))
	var missing []string
	for _, key := range writes {
		raw, ok := obj[key]
		if !ok {
			missing = append(missing, key)
			continue
		}
		updates[key] = rawJSONValueToContext(raw)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, nil, fmt.Errorf("missing keys in response JSON: %s", strings.Join(missing, ", "))
	}

	declared := make(map[string]struct{}, len(writes))
	for _, key := range writes {
		declared[key] = struct{}{}
	}
	for key := range obj {
		if _, ok := declared[key]; !ok {
			extras = append(extras, key)
		}
	}
	sort.Strings(extras)
	return updates, extras, nil
}

func normalizeDeclaredKeys(keys []string) []string {
	var out []string
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}

func rawJSONValueToContext(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}
