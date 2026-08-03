// ABOUTME: Small pure helpers extracted from session_run.go to keep that file and its functions under the complexity ratchet.
// ABOUTME: Conversation-init assembly (system prompt, prior-episode summaries, user input) and per-tool-call dispatch bookkeeping.
package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/2389-research/tracker/agent/tools"
	"github.com/2389-research/tracker/llm"
)

// assembleSystemPrompt builds the system message text.
//
// tool_access enforcement (issue #258): when restricted, swap the built-in
// basePrompt for a tool-free variant. The default basePrompt names
// read/write/edit/glob/grep_search/bash explicitly to disambiguate path
// semantics — under tool_access restriction the agent has no tools so the
// disambiguation is irrelevant and the prompt would only tell the LLM what
// tools to ask for.
//
// Note: this scrub applies ONLY to the built-in prefix. If the caller also
// supplies SessionConfig.SystemPrompt that names tools, those names are still
// appended verbatim. The registry-empty + ToolChoice=none + dispatch-
// shortcircuit defenses do NOT depend on the prompt scrub; the scrub is
// defense-in-depth, not the load-bearing check.
func (s *Session) assembleSystemPrompt() string {
	basePrompt := "File tool arguments (read, write, edit, glob, grep_search) MUST use paths relative to the working directory. " +
		"For example, use \"src/main.go\" instead of \"/home/user/project/src/main.go\". " +
		"Bash commands may use absolute paths when needed. " +
		"Use the report_status tool to narrate your progress in plain language at meaningful moments — " +
		"starting or finishing a milestone/phase, or a notable result — a short, honest \"what I'm doing / just " +
		"finished, and where I am in the job\". Do this as part of work you're already doing, not every turn, and " +
		"describe the actual work rather than restating the step name."
	if s.config.IsToolAccessRestricted() {
		basePrompt = "Respond to the user with plain text only; do not attempt to invoke any tool. " +
			"No tools are available for this session."
	}
	if s.config.SystemPrompt != "" {
		return basePrompt + "\n\n" + s.config.SystemPrompt
	}
	return basePrompt
}

// appendPriorEpisodeSummaries injects a user message summarizing earlier
// attempts, skipping empty summaries and empty lines. No-op when there are no
// non-empty summaries.
func (s *Session) appendPriorEpisodeSummaries() {
	if len(s.config.PriorEpisodeSummaries) == 0 {
		return
	}
	nonEmpty := make([]string, 0, len(s.config.PriorEpisodeSummaries))
	for _, summary := range s.config.PriorEpisodeSummaries {
		if trimmed := strings.TrimSpace(summary); trimmed != "" {
			nonEmpty = append(nonEmpty, trimmed)
		}
	}
	if len(nonEmpty) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString("Prior attempts summary (avoid repeating failed approaches):\n")
	for i, summary := range nonEmpty {
		b.WriteString(fmt.Sprintf("Attempt %d:\n", i+1))
		writeSummaryLines(&b, summary)
	}
	s.messages = append(s.messages, llm.UserMessage(strings.TrimSpace(b.String())))
}

// writeSummaryLines appends each non-empty line of summary as a bullet.
func writeSummaryLines(b *strings.Builder, summary string) {
	for _, line := range strings.Split(summary, "\n") {
		if trimmedLine := strings.TrimSpace(line); trimmedLine != "" {
			b.WriteString(fmt.Sprintf("  - %s\n", trimmedLine))
		}
	}
}

// assembleUserInput prepends the localization block to the user input when
// Localize is enabled and the scan produced one.
func (s *Session) assembleUserInput(ctx context.Context, userInput string) string {
	if s.config.Localize {
		if block := localize(ctx, s.config.WorkingDir, userInput).Message; block != "" {
			return block + "\n" + userInput
		}
	}
	return userInput
}

// runOneToolCall executes a single tool call, records it, emits the
// EventToolCallEnd event, and returns the content part plus whether the call
// errored and whether it was a terminal-tool success. Extracted from
// executeToolCalls for readability and complexity.
func (s *Session) runOneToolCall(ctx context.Context, call llm.ToolCallData, turn int, result *SessionResult) (llm.ContentPart, bool, bool) {
	toolResult, toolDuration := s.executeSingleTool(ctx, call, turn)
	result.ToolCalls[call.Name]++
	s.episodeLog.Record(call.Name, string(call.Arguments), toolResult.Content, toolResult.IsError)

	terminate := s.isTerminalSuccess(call, toolResult)

	s.emit(Event{
		Type:         EventToolCallEnd,
		SessionID:    s.id,
		Turn:         turn,
		ToolName:     call.Name,
		ToolInput:    string(call.Arguments),
		ToolOutput:   toolResult.Content,
		ToolError:    boolToErrStr(toolResult.IsError),
		ToolDuration: toolDuration,
	})

	return llm.ContentPart{Kind: llm.KindToolResult, ToolResult: &toolResult}, toolResult.IsError, terminate
}

// isTerminalSuccess reports whether a tool call succeeded AND the tool flags
// itself terminal. Errors keep the loop alive so the model can react to the
// failure.
func (s *Session) isTerminalSuccess(call llm.ToolCallData, res llm.ToolResultData) bool {
	if res.IsError {
		return false
	}
	tool := s.registry.Get(call.Name)
	return tool != nil && tools.IsToolTerminal(tool)
}
