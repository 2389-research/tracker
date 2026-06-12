// ABOUTME: Variable expansion and context injection for pipeline node attributes.
// ABOUTME: Expands $goal, graph-level variables, and appends prior node outputs to LLM prompts.
package pipeline

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

func ExpandPromptVariables(prompt string, ctx *PipelineContext) string {
	if prompt == "" || ctx == nil {
		return prompt
	}
	if goal, ok := ctx.Get(ContextKeyGoal); ok {
		prompt = strings.ReplaceAll(prompt, "$goal", goal)
	}
	return prompt
}

// GraphVarMap extracts graph-level variables from the pipeline context as a
// $key → value map. Call once per node and pass the result to ExpandGraphVariables
// to avoid repeated Snapshot() copies.
func GraphVarMap(ctx *PipelineContext) map[string]string {
	if ctx == nil {
		return nil
	}
	vars := make(map[string]string)
	for key, val := range ctx.Snapshot() {
		if strings.HasPrefix(key, "graph.") {
			vars["$"+strings.TrimPrefix(key, "graph.")] = val
		}
	}
	return vars
}

// ExpandGraphVariables substitutes $key references in text with values from
// graph-level attributes. The vars map should come from GraphVarMap.
// For example, graph[target_name="foo"] expands $target_name to "foo".
// This applies to any node attribute (prompt, tool_command, etc.) so all
// handlers get uniform variable expansion.
func ExpandGraphVariables(text string, vars map[string]string) string {
	if text == "" || len(vars) == 0 || !strings.Contains(text, "$") {
		return text
	}
	// Try longest names first at each position so $target never clobbers
	// $target_name — map iteration order is random, which made prefix
	// collisions flaky.
	names := make([]string, 0, len(vars))
	for varName := range vars {
		names = append(names, varName)
	}
	sort.Slice(names, func(i, j int) bool {
		if len(names[i]) != len(names[j]) {
			return len(names[i]) > len(names[j])
		}
		return names[i] < names[j]
	})
	// Single left-to-right pass: substituted values are appended to the
	// output and never re-scanned, so a value containing a $name literal
	// can't be expanded a second time (CLAUDE.md: variable expansion is
	// single-pass — never re-scan resolved values). The previous
	// sequential ReplaceAll loop re-scanned earlier substitutions.
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); {
		if text[i] != '$' {
			b.WriteByte(text[i])
			i++
			continue
		}
		matched := false
		for _, varName := range names {
			if strings.HasPrefix(text[i:], varName) {
				b.WriteString(vars[varName])
				i += len(varName)
				matched = true
				break
			}
		}
		if !matched {
			b.WriteByte(text[i])
			i++
		}
	}
	return b.String()
}

// DefaultInjectedResponseCap is the default byte budget for the
// "Previous Node Output" section appended by InjectPipelineContext (#352).
// Without a cap, a prior node's full transcript (verification reports, grep
// tables) is pasted wholesale into every downstream prompt — wasteful and,
// when the prior node failed, actively mis-anchoring. The cap applies at
// prompt-injection time only; the stored context value (and therefore
// node.<id>.last_response scoping and checkpoints) keeps the full value.
const DefaultInjectedResponseCap = 4096

// contextKeysForInjection lists the pipeline context keys whose values should
// be appended to the LLM prompt so that downstream nodes can see prior outputs.
// capped marks keys subject to the injection byte budget: last_response is an
// LLM transcript and gets head+tail truncation; human_response is human-typed
// and injected whole.
var contextKeysForInjection = []struct {
	key    string
	label  string
	capped bool
}{
	{ContextKeyHumanResponse, "Human Response", false},
	{ContextKeyLastResponse, "Previous Node Output", true},
}

// capHeadTail truncates s to roughly capBytes by keeping the head and tail
// halves and replacing the middle with an explicit elision marker. Cuts land
// on UTF-8 rune boundaries. Head+tail (not tail-only like tool output capture)
// because prose transcripts carry the task framing up front and the
// conclusion at the end; there is no end-of-output routing marker to protect.
func capHeadTail(s string, capBytes int) string {
	if capBytes <= 0 || len(s) <= capBytes {
		return s
	}
	headEnd := capBytes / 2
	for headEnd > 0 && !utf8.RuneStart(s[headEnd]) {
		headEnd--
	}
	tailStart := len(s) - (capBytes - headEnd)
	for tailStart < len(s) && !utf8.RuneStart(s[tailStart]) {
		tailStart++
	}
	elided := tailStart - headEnd
	return s[:headEnd] +
		fmt.Sprintf("\n\n[... tracker elided %d of %d bytes from the middle of this output ...]\n\n", elided, len(s)) +
		s[tailStart:]
}

// InjectPipelineContext appends relevant pipeline context values to the prompt
// so the LLM can see prior node outputs, human responses, etc.
//
// capBytes bounds the injected "Previous Node Output" section: 0 applies
// DefaultInjectedResponseCap, negative disables capping, positive values are
// used as-is. Oversized values are head+tail truncated with an explicit
// elision marker (see capHeadTail).
func InjectPipelineContext(prompt string, ctx *PipelineContext, capBytes int) string {
	if ctx == nil {
		return prompt
	}
	if capBytes == 0 {
		capBytes = DefaultInjectedResponseCap
	}

	var sections []string
	for _, entry := range contextKeysForInjection {
		if val, ok := ctx.Get(entry.key); ok && val != "" {
			if entry.capped {
				val = capHeadTail(val, capBytes)
			}
			sections = append(sections, fmt.Sprintf("## %s\n%s", entry.label, val))
		}
	}

	if len(sections) == 0 {
		return prompt
	}

	return prompt + "\n\n---\n# Context from Prior Pipeline Stages\n\n" + strings.Join(sections, "\n\n")
}
