// ABOUTME: report_status — the agent narrates high-level progress in its own words,
// ABOUTME: piggybacked on a normal turn (no dedicated LLM call), surfaced as a first-class event (#494).
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ReportStatusTool lets the agent emit a plain-language status update — "what I'm
// doing / what I just finished / where I am in the job" — as a first-class event,
// distinct from the turn/tool firehose. It's cheap by design: the agent calls it
// as part of work it's already doing, and it makes no LLM call itself.
type ReportStatusTool struct {
	// emit forwards the status text to the session's event stream. Injected by
	// the session so the tool stays decoupled from the agent package.
	emit func(status string)
}

// NewReportStatusTool builds the tool with the session's status-emit callback.
func NewReportStatusTool(emit func(status string)) *ReportStatusTool {
	return &ReportStatusTool{emit: emit}
}

func (t *ReportStatusTool) Name() string { return "report_status" }

func (t *ReportStatusTool) Description() string {
	return "Report a short, honest, high-level status of your progress — what you're doing right now, " +
		"what you just accomplished, and where you are in the overall job (e.g. \"milestone 3 of 7\"). " +
		"Call this at meaningful moments (starting/finishing a milestone or phase, or a notable result), " +
		"NOT every turn. One or two plain sentences. Describe the actual work, not a restatement of the step name."
}

func (t *ReportStatusTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"status": {
				"type": "string",
				"description": "One or two plain-language sentences: what you're doing / just finished, and where you are in the job."
			}
		},
		"required": ["status"]
	}`)
}

// CachePolicy: never cache — a status is a point-in-time side effect.
func (t *ReportStatusTool) CachePolicy() CachePolicy { return CachePolicyMutating }

func (t *ReportStatusTool) Execute(_ context.Context, input json.RawMessage) (string, error) {
	var args struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("report_status: invalid arguments: %w", err)
	}
	status := strings.TrimSpace(args.Status)
	if status == "" {
		return "", fmt.Errorf("report_status: status must not be empty")
	}
	if t.emit != nil {
		t.emit(status)
	}
	return "status recorded", nil
}
