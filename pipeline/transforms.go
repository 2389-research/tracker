package pipeline

import "strings"

func ExpandPromptVariables(prompt string, ctx *PipelineContext) string {
	if prompt == "" || ctx == nil {
		return prompt
	}
	if goal, ok := ctx.Get(ContextKeyGoal); ok {
		prompt = strings.ReplaceAll(prompt, "$goal", goal)
	}
	return prompt
}
