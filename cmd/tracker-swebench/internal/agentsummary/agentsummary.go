// ABOUTME: The JSON summary agent-runner prints as its last stdout line and the swebench harness parses.
// ABOUTME: Both binaries import this one definition, so the encoder and the decoder cannot drift apart.
package agentsummary

// MaxFinalMessageRunes caps Summary.FinalMessage.
const MaxFinalMessageRunes = 400

// Summary holds token usage and timing stats that agent-runner prints as its
// last stdout line and the harness extracts from that output.
type Summary struct {
	Turns             int      `json:"turns"`
	InputTokens       int64    `json:"input_tokens"`
	OutputTokens      int64    `json:"output_tokens"`
	DurationMs        int64    `json:"duration_ms"`
	TerminationReason string   `json:"termination_reason"`
	FinalMessage      string   `json:"final_message"`
	LastToolCalls     []string `json:"last_tool_calls"`
}

// TruncateRunes cuts s to at most max runes without adding an ellipsis. A max
// of zero or less yields the empty string.
func TruncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// NormalizeLastToolCalls returns a copy of the last three calls. It never
// returns nil, so an empty list encodes as [] rather than null.
func NormalizeLastToolCalls(calls []string) []string {
	if len(calls) > 3 {
		calls = calls[len(calls)-3:]
	}
	if len(calls) == 0 {
		return []string{}
	}
	out := make([]string, len(calls))
	copy(out, calls)
	return out
}
