// ABOUTME: Parses the free text of an @mention into a workflow + params (decision D1).
package main

import (
	"fmt"
	"strings"
)

// Intent is a parsed request: which workflow to run and any param overrides.
type Intent struct {
	Workflow string
	Params   map[string]string
}

// resolveIntent turns the free text of an @mention into an Intent.
//
// DECISION POINT (D1) — this starter is a simple grammar:
//
//	[run] <workflow> [key=value ...]
//
// Refine it however fits: an LLM classifier is the natural match for
// "make me an app that …" (free text → a built-in workflow + params), or keep a
// grammar, or a hybrid (LLM with this grammar as a fast-path). tracker.Workflows()
// lists the built-ins to map onto.
func resolveIntent(text string) (Intent, error) {
	fields := strings.Fields(stripMention(text))
	if len(fields) > 0 && strings.EqualFold(fields[0], "run") {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return Intent{}, fmt.Errorf("which workflow should I run?")
	}
	in := Intent{Workflow: fields[0], Params: map[string]string{}}
	for _, f := range fields[1:] {
		if k, v, ok := strings.Cut(f, "="); ok {
			in.Params[k] = v
		}
	}
	return in, nil
}

// stripMention drops a leading Slack <@BOTID> mention token if present.
func stripMention(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "<@") {
		if i := strings.Index(text, ">"); i >= 0 {
			return strings.TrimSpace(text[i+1:])
		}
	}
	return text
}
