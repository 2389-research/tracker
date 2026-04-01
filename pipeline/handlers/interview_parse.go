// ABOUTME: Pure function for extracting structured questions from agent markdown output.
// ABOUTME: Used by the human handler to parse upstream agent output into form fields.
package handlers

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Question represents a parsed interview question.
type Question struct {
	Index   int      // 1-based ordinal
	Text    string   // Question text (markdown stripped for labels, code spans preserved)
	Context string   // Optional context/rationale for the question
	Options []string // Inline options from trailing parentheticals; empty for open-ended
	IsYesNo bool     // Detected yes/no pattern
}

// structuredQuestions is the JSON schema agents should output for interview questions.
type structuredQuestions struct {
	Questions []structuredQuestion `json:"questions"`
}

type structuredQuestion struct {
	Text    string   `json:"text"`
	Context string   `json:"context,omitempty"`
	Options []string `json:"options,omitempty"`
}

// ParseStructuredQuestions attempts to parse JSON-formatted questions from agent output.
// Returns parsed questions and nil error on success. Returns nil and an error if the
// input is not valid structured JSON or fails validation.
//
// Expected JSON format:
//
//	{"questions": [{"text": "Auth model?", "context": "We found 3 auth patterns", "options": ["API key", "OAuth"]}]}
//
// The JSON may be wrapped in markdown code fences (```json ... ```).
func ParseStructuredQuestions(input string) ([]Question, error) {
	if input == "" {
		return nil, fmt.Errorf("empty input")
	}

	// Strip markdown code fences if present.
	jsonStr := extractJSON(input)
	if jsonStr == "" {
		return nil, fmt.Errorf("no JSON object found in input")
	}

	var sq structuredQuestions
	if err := json.Unmarshal([]byte(jsonStr), &sq); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	if len(sq.Questions) == 0 {
		return nil, fmt.Errorf("questions array is empty")
	}
	if len(sq.Questions) > maxQuestions {
		return nil, fmt.Errorf("too many questions (%d > %d)", len(sq.Questions), maxQuestions)
	}

	// Validate and convert.
	questions := make([]Question, 0, len(sq.Questions))
	for i, sq := range sq.Questions {
		text := strings.TrimSpace(sq.Text)
		if text == "" {
			return nil, fmt.Errorf("question %d has empty text", i+1)
		}
		filtered := filterOtherOption(sq.Options)
		q := Question{
			Index:   i + 1,
			Text:    text,
			Context: strings.TrimSpace(sq.Context),
			Options: filtered,
			IsYesNo: isYesNoQuestion(filtered, text),
		}
		questions = append(questions, q)
	}

	return questions, nil
}

// extractJSON finds the first complete JSON object in text using bracket-depth
// counting. This correctly handles multiple JSON objects in the text (returns
// only the first) and ignores braces inside string literals.
func extractJSON(text string) string {
	// Try stripping code fences first.
	stripped := stripCodeFences(text)

	start := strings.Index(stripped, "{")
	if start == -1 {
		return ""
	}

	depth := 0
	inString := false
	escaped := false

	for i := start; i < len(stripped); i++ {
		ch := stripped[i]

		if escaped {
			escaped = false
			continue
		}

		if ch == '\\' && inString {
			escaped = true
			continue
		}

		if ch == '"' {
			inString = !inString
			continue
		}

		if inString {
			continue
		}

		if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
			if depth == 0 {
				return stripped[start : i+1]
			}
		}
	}

	return ""
}

// stripCodeFences removes ```json ... ``` wrappers from agent output.
func stripCodeFences(text string) string {
	lines := strings.Split(text, "\n")
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			continue // skip fence markers
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

var (
	reNumbered   = regexp.MustCompile(`^\s*\d+[.)]\s+(.+)`)
	reBulletQ    = regexp.MustCompile(`^\s*[-*]\s+(.+\?)\s*$`)
	reImperative = regexp.MustCompile(`(?i)^\s*[-*]?\s*(describe|explain|list|specify|provide|choose|select|confirm|rate|rank)\b`)
	reOptions    = regexp.MustCompile(`\(([^)]+)\)\s*$`)
	reFence      = regexp.MustCompile("^\\s*```")
	reEmphasis   = regexp.MustCompile(`\*{1,2}([^*]+)\*{1,2}`)
	reUnderline  = regexp.MustCompile(`_{1,2}([^_]+)_{1,2}`)
)

// maxQuestions is the upper bound on questions parsed from a single markdown
// document. This prevents unbounded allocation from adversarial agent output.
const maxQuestions = 100

// ParseQuestions extracts questions from upstream agent markdown output.
// It returns a slice of Question structs with 1-based indices.
// Content inside fenced code blocks is skipped. At most maxQuestions are returned.
func ParseQuestions(markdown string) []Question {
	if markdown == "" {
		return nil
	}

	lines := strings.Split(markdown, "\n")
	var questions []Question
	inFence := false
	index := 0

	for _, line := range lines {
		// Track fenced code block state
		if reFence.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}

		text, matched := matchQuestion(line)
		if !matched {
			continue
		}

		// Strip markdown emphasis from question text for clean labels.
		text = stripEmphasis(text)

		// Extract trailing options parenthetical
		var options []string
		if m := reOptions.FindStringSubmatch(text); m != nil {
			options = splitOptions(m[1])
			// Strip the parenthetical from the text
			text = strings.TrimSpace(reOptions.ReplaceAllString(text, ""))
		}

		index++
		options = filterOtherOption(options)
		questions = append(questions, Question{
			Index:   index,
			Text:    text,
			Options: options,
			IsYesNo: isYesNoQuestion(options, text),
		})

		if index >= maxQuestions {
			break
		}
	}

	return questions
}

// matchQuestion returns the question text and true if the line matches any
// question heuristic: numbered list, bullet ending in ?, or imperative verb.
func matchQuestion(line string) (string, bool) {
	// Numbered: "1. ..." or "1) ..."
	if m := reNumbered.FindStringSubmatch(line); m != nil {
		return strings.TrimSpace(m[1]), true
	}

	// Imperative verb (optionally preceded by bullet): "Describe ...", "- List ..."
	if reImperative.MatchString(line) {
		// Strip leading bullet/whitespace
		text := strings.TrimSpace(line)
		text = strings.TrimLeft(text, "-* \t")
		text = strings.TrimSpace(text)
		return text, true
	}

	// Bullet ending in "?": "- What auth model?"
	if m := reBulletQ.FindStringSubmatch(line); m != nil {
		return strings.TrimSpace(m[1]), true
	}

	// Bare line ending in "?" (not a bullet, not numbered) — e.g. "Scale? (low, high)"
	trimmed := strings.TrimSpace(line)
	if strings.Contains(trimmed, "?") {
		// Accept lines that contain a "?" possibly followed by a parenthetical
		// but skip purely commentary lines
		baseText := reOptions.ReplaceAllString(trimmed, "")
		baseText = strings.TrimSpace(baseText)
		if strings.HasSuffix(baseText, "?") {
			return trimmed, true
		}
	}

	return "", false
}

// splitOptions splits a comma-separated option string on ", " (comma-space).
// Falls back to "," when no comma-space separator is found but multiple commas exist.
func splitOptions(raw string) []string {
	// Prefer ", " as the separator to avoid splitting within descriptions
	var parts []string
	if strings.Contains(raw, ", ") {
		parts = strings.Split(raw, ", ")
	} else {
		parts = strings.Split(raw, ",")
	}

	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// stripEmphasis removes markdown bold/italic markers from text,
// preserving code spans (backtick-wrapped content).
func stripEmphasis(text string) string {
	text = reEmphasis.ReplaceAllString(text, "$1")
	text = reUnderline.ReplaceAllString(text, "$1")
	return text
}

// isYesNoQuestion returns true when the options are exactly [yes, no]
// (case-insensitive), or when there are no explicit options and the question
// text matches common yes/no interrogative patterns.
func isYesNoQuestion(options []string, text string) bool {
	if len(options) == 2 {
		a := strings.ToLower(strings.TrimSpace(options[0]))
		b := strings.ToLower(strings.TrimSpace(options[1]))
		if (a == "yes" && b == "no") || (a == "no" && b == "yes") {
			return true
		}
	}
	// Text-based detection only applies when there are no explicit options,
	// since explicit options override the inferred answer type.
	if len(options) > 0 {
		return false
	}
	// "Do/Does/Is/Are/Can/Will/Would/Should ..."
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, prefix := range []string{"do ", "does ", "is ", "are ", "can ", "will ", "would ", "should ", "have ", "has "} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// filterOtherOption removes "other" variants from options since the interview UI
// always provides its own "Other" escape hatch with a freeform textarea.
// Matches "other" exactly, or "other" followed by any non-alphanumeric character
// (space, dash, colon, paren, comma, em-dash, etc.), catching patterns like
// "other (please specify)", "other: specify", "other - describe", etc.
func filterOtherOption(options []string) []string {
	out := make([]string, 0, len(options))
	for _, opt := range options {
		lower := strings.ToLower(strings.TrimSpace(opt))
		if isOtherVariant(lower) {
			continue
		}
		out = append(out, opt)
	}
	return out
}

// isOtherVariant returns true if the lowered, trimmed string is "other" or
// starts with "other" followed by a non-alphanumeric character.
func isOtherVariant(lower string) bool {
	if lower == "other" {
		return true
	}
	if !strings.HasPrefix(lower, "other") {
		return false
	}
	// Check that the character after "other" is non-alphanumeric.
	rest := lower[len("other"):]
	if len(rest) == 0 {
		return true
	}
	ch := rest[0]
	// Alphanumeric: a-z, 0-9 (already lowered)
	if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
		return false
	}
	return true
}
