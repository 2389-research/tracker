package pipeline

import (
	"fmt"
	"strings"
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

// ExpandGraphVariables substitutes $key references in text with values from
// graph-level attributes stored in the pipeline context as "graph.<key>".
// For example, graph[target_name="foo"] stored as "graph.target_name" expands
// $target_name to "foo". This applies to any node attribute (prompt,
// tool_command, etc.) so all handlers get uniform variable expansion.
func ExpandGraphVariables(text string, ctx *PipelineContext) string {
	if text == "" || ctx == nil || !strings.Contains(text, "$") {
		return text
	}
	for key, val := range ctx.Snapshot() {
		if !strings.HasPrefix(key, "graph.") {
			continue
		}
		varName := "$" + strings.TrimPrefix(key, "graph.")
		if strings.Contains(text, varName) {
			text = strings.ReplaceAll(text, varName, val)
		}
	}
	return text
}

// contextKeysForInjection lists the pipeline context keys whose values should
// be appended to the LLM prompt so that downstream nodes can see prior outputs.
var contextKeysForInjection = []struct {
	key   string
	label string
}{
	{ContextKeyHumanResponse, "Human Response"},
	{ContextKeyLastResponse, "Previous Node Output"},
}

// InjectPipelineContext appends relevant pipeline context values to the prompt
// so the LLM can see prior node outputs, human responses, etc.
func InjectPipelineContext(prompt string, ctx *PipelineContext) string {
	if ctx == nil {
		return prompt
	}

	var sections []string
	for _, entry := range contextKeysForInjection {
		if val, ok := ctx.Get(entry.key); ok && val != "" {
			sections = append(sections, fmt.Sprintf("## %s\n%s", entry.label, val))
		}
	}

	if len(sections) == 0 {
		return prompt
	}

	return prompt + "\n\n---\n# Context from Prior Pipeline Stages\n\n" + strings.Join(sections, "\n\n")
}
