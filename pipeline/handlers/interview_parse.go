// ABOUTME: Pure function for extracting structured questions from agent markdown output.
// ABOUTME: Used by the human handler to parse upstream agent output into form fields.
package handlers

import (
	"regexp"
	"strings"
)

// Question represents a parsed interview question.
type Question struct {
	Index   int      // 1-based ordinal
	Text    string   // Question text (markdown stripped for labels, code spans preserved)
	Options []string // Inline options from trailing parentheticals; empty for open-ended
	IsYesNo bool     // Detected yes/no pattern
}

var (
	reNumbered   = regexp.MustCompile(`^\s*\d+[.)]\s+(.+)`)
	reBulletQ    = regexp.MustCompile(`^\s*[-*]\s+(.+\?)\s*$`)
	reImperative = regexp.MustCompile(`(?i)^\s*[-*]?\s*(describe|explain|list|specify|provide|choose|select|confirm|rate|rank)\b`)
	reOptions    = regexp.MustCompile(`\(([^)]+)\)\s*$`)
	reFence      = regexp.MustCompile("^\\s*```")
)

// ParseQuestions extracts questions from upstream agent markdown output.
// It returns a slice of Question structs with 1-based indices.
// Content inside fenced code blocks is skipped.
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

		// Extract trailing options parenthetical
		var options []string
		if m := reOptions.FindStringSubmatch(text); m != nil {
			options = splitOptions(m[1])
			// Strip the parenthetical from the text
			text = strings.TrimSpace(reOptions.ReplaceAllString(text, ""))
		}

		index++
		questions = append(questions, Question{
			Index:   index,
			Text:    text,
			Options: options,
			IsYesNo: isYesNoQuestion(options, text),
		})
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
