package pipeline

import (
	"bytes"
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

// ExtractJSONFromText tries to find a valid JSON object embedded in text.
// It first looks for ```json fenced blocks, then falls back to finding the
// outermost { ... } substring. Returns the extracted JSON string and true
// if a valid JSON object was found, or ("", false) otherwise.
func ExtractJSONFromText(text string) (string, bool) {
	// Try fenced JSON blocks first (```json ... ``` or ``` ... ```)
	if extracted := extractFencedJSON(text); extracted != "" {
		return extracted, true
	}
	// Try outermost braces
	if extracted := extractOutermostJSON(text); extracted != "" {
		return extracted, true
	}
	return "", false
}

// extractFencedJSON looks for a ```json or ``` fenced block containing valid JSON.
func extractFencedJSON(text string) string {
	fence := "```"
	start := strings.Index(text, fence)
	if start < 0 {
		return ""
	}
	// Skip past the opening fence and any language tag on the same line
	contentStart := strings.Index(text[start+len(fence):], "\n")
	if contentStart < 0 {
		return ""
	}
	contentStart += start + len(fence) + 1 // +1 for the newline

	// Find closing fence
	end := strings.Index(text[contentStart:], fence)
	if end < 0 {
		return ""
	}
	candidate := strings.TrimSpace(text[contentStart : contentStart+end])
	if candidate == "" {
		return ""
	}
	// Validate it's a JSON object
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(candidate), &obj); err != nil {
		return ""
	}
	return candidate
}

// extractOutermostJSON finds the first '{' and last '}' in the text and
// checks whether the substring between them is a valid JSON object.
func extractOutermostJSON(text string) string {
	first := strings.IndexByte(text, '{')
	if first < 0 {
		return ""
	}
	last := strings.LastIndexByte(text, '}')
	if last <= first {
		return ""
	}
	candidate := text[first : last+1]
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(candidate), &obj); err != nil {
		return ""
	}
	return candidate
}

func rawJSONValueToContext(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err == nil {
		return compact.String()
	}
	return string(raw)
}
