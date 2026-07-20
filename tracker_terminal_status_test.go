// ABOUTME: Tests for the authoritative terminal-status event (#475).
// ABOUTME: A run's terminal event carries TerminalStatus on the pipeline stream and NDJSON.
package tracker

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

func successStub() *stubCompleter {
	return &stubCompleter{response: &llm.Response{
		Message:      llm.AssistantMessage("done"),
		FinishReason: llm.FinishReason{Reason: "stop"},
	}}
}

// TestRun_EmitsTerminalStatusOnCompletion asserts exactly one event on the
// pipeline stream carries TerminalStatus and it is the completion event.
func TestRun_EmitsTerminalStatusOnCompletion(t *testing.T) {
	var mu sync.Mutex
	var terminal []pipeline.PipelineEvent

	_, err := Run(context.Background(), gateDip, Config{
		Format:      "dip",
		WorkingDir:  t.TempDir(),
		LLMClient:   successStub(),
		Interviewer: &recordingInterviewer{answer: "go"},
		EventHandler: pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
			if evt.TerminalStatus != "" {
				mu.Lock()
				terminal = append(terminal, evt)
				mu.Unlock()
			}
		}),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(terminal) != 1 {
		t.Fatalf("expected exactly one event with a terminal status, got %d", len(terminal))
	}
	if terminal[0].Type != pipeline.EventPipelineCompleted {
		t.Fatalf("terminal event type = %q, want %q", terminal[0].Type, pipeline.EventPipelineCompleted)
	}
	if got := terminal[0].TerminalStatus; got != string(pipeline.OutcomeSuccess) {
		t.Fatalf("TerminalStatus = %q, want %q", got, pipeline.OutcomeSuccess)
	}
}

// TestNDJSON_CarriesTerminalStatus asserts the NDJSON wire format surfaces the
// terminal status on the completion line.
func TestNDJSON_CarriesTerminalStatus(t *testing.T) {
	var buf bytes.Buffer
	w := NewNDJSONWriter(&buf)

	_, err := Run(context.Background(), gateDip, Config{
		Format:       "dip",
		WorkingDir:   t.TempDir(),
		LLMClient:    successStub(),
		Interviewer:  &recordingInterviewer{answer: "go"},
		EventHandler: w.PipelineHandler(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var found string
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var evt StreamEvent
		if uerr := json.Unmarshal([]byte(line), &evt); uerr != nil {
			t.Fatalf("unmarshal NDJSON line %q: %v", line, uerr)
		}
		if evt.TerminalStatus != "" {
			if found != "" {
				t.Fatalf("more than one line carried terminal_status")
			}
			found = evt.TerminalStatus
			if evt.Type != string(pipeline.EventPipelineCompleted) {
				t.Fatalf("terminal line type = %q, want pipeline_completed", evt.Type)
			}
		}
	}
	if found != string(pipeline.OutcomeSuccess) {
		t.Fatalf("terminal_status on NDJSON = %q, want %q", found, pipeline.OutcomeSuccess)
	}
}
