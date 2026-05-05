package pipeline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
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

// ExtractJSONFromText tries to find a valid JSON OBJECT embedded in text. It
// iterates ```...``` fenced blocks first (returning the first whose content
// parses as a JSON object — so a non-JSON fence ahead of a valid JSON fence
// doesn't block discovery), then falls back to scanning for top-level {…}
// spans (preferring the first balanced span that parses, not the
// outermost-brace shortcut, which fails when prose contains stray brace
// pairs around real JSON).
//
// Returns the extracted JSON string and true if a valid JSON object was
// found, or ("", false) otherwise.
//
// Intended for use by pipeline handlers; the Go-package boundary requires
// it to be exported, but it isn't part of the stable embedder API.
func ExtractJSONFromText(text string) (string, bool) {
	if extracted := extractFencedJSON(text); extracted != "" {
		return extracted, true
	}
	if extracted := extractBracedJSON(text); extracted != "" {
		return extracted, true
	}
	return "", false
}

// fencedBlockRE matches a Markdown-style fenced code block. The opening
// fence is followed by an optional language tag (alphanumerics, '_', '-',
// '+', '.') and trailing whitespace up to a newline — this strict shape
// distinguishes a real opening fence from stray backticks in prose like
// "Use ``` to denote code". (?s) lets `.` cross newlines; the `+?` is
// non-greedy so we capture the smallest body up to the next ```.
var fencedBlockRE = regexp.MustCompile("(?s)```[A-Za-z0-9_+.\\-]*[ \\t]*\\r?\\n(.+?)```")

// extractFencedJSON walks every ```...``` fenced code block in text and
// returns the first one whose content parses as a JSON object. Iterating
// (rather than stopping at the first match) is necessary because LLMs
// commonly emit a ```text or ```bash preamble before the answer; stopping
// at the first fence would silently drop the real result.
//
// The opening fence is required to have the canonical "fence + optional
// language tag + newline" shape, so stray inline backticks in prose don't
// kick off extraction in the wrong place. (Without that constraint, an
// odd number of stray fences in the input would misalign every subsequent
// pair.)
func extractFencedJSON(text string) string {
	for _, m := range fencedBlockRE.FindAllStringSubmatch(text, -1) {
		if c := strings.TrimSpace(m[1]); c != "" && isJSONObject(c) {
			return c
		}
	}
	return ""
}

// extractBracedJSON scans text for the first balanced top-level {…} span
// that parses as a JSON object. Unlike a first-`{` / last-`}` shortcut,
// this handles prose like:
//
//	"Wrote {file1} and {file2}, then produced {\"x\": 1}. Done."
//
// where the outermost-brace approach picks an invalid span and returns
// nothing even though a real JSON object is present.
//
// Top-level JSON arrays are NOT decomposed: `[{"a":1}]` returns "" because
// the user supplied an array, not an object. Inner braces inside string
// literals and arrays are skipped via state tracking so they can't kick off
// an extraction.
func extractBracedJSON(text string) string {
	inString := false
	escaped := false
	arrayDepth := 0
	for i := 0; i < len(text); i++ {
		c := text[i]
		if escaped {
			escaped = false
			continue
		}
		if inString {
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '[':
			arrayDepth++
		case ']':
			if arrayDepth > 0 {
				arrayDepth--
			}
		case '{':
			if arrayDepth > 0 {
				// Inside a JSON array — skip; we don't decompose arrays.
				continue
			}
			end, ok := matchBalancedBrace(text, i)
			if !ok {
				continue
			}
			candidate := text[i : end+1]
			if isJSONObject(candidate) {
				return candidate
			}
		}
	}
	return ""
}

// matchBalancedBrace returns the index of the '}' that closes the '{' at
// start, or (0, false) if the braces are unbalanced. JSON-string content
// is honored — a '{' or '}' inside a "..." string doesn't affect the depth.
func matchBalancedBrace(text string, start int) (int, bool) {
	if start >= len(text) || text[start] != '{' {
		return 0, false
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		c := text[i]
		if escaped {
			escaped = false
			continue
		}
		if inString {
			switch c {
			case '\\':
				escaped = true
			case '"':
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
				return i, true
			}
		}
	}
	return 0, false
}

// isJSONObject reports whether s parses as a JSON object (not an array,
// scalar, or null). Used to gate extraction-helper return values.
func isJSONObject(s string) bool {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "{") {
		return false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &obj); err != nil {
		return false
	}
	return obj != nil
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
