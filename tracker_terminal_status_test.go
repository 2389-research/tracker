// ABOUTME: Tests for the authoritative terminal-status event (#475).
// ABOUTME: A run's terminal event carries TerminalStatus on the pipeline stream and NDJSON.
package tracker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// failingCompleter returns a hard error from every completion, to drive the
// handler-error terminal path (handleNodeError).
type failingCompleter struct{ err error }

func (f *failingCompleter) Complete(_ context.Context, _ *llm.Request) (*llm.Response, error) {
	return nil, f.err
}

// collectTerminal returns an EventHandler that records every event carrying a
// non-empty TerminalStatus, plus a pointer to the slice.
func collectTerminal(mu *sync.Mutex, out *[]pipeline.PipelineEvent) pipeline.PipelineEventHandler {
	return pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
		if evt.TerminalStatus != "" {
			mu.Lock()
			*out = append(*out, evt)
			mu.Unlock()
		}
	})
}

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

// TestRun_EmitsTerminalStatusOnFailure asserts a failing run (here a provider
// hard-fail) emits exactly one terminal event carrying TerminalStatus=fail —
// the run-finished signal a stream-only subscriber (e.g. Slack) relies on for
// the common failure mode. A failure halt returns the fail result with a nil
// Run error, so the terminal event on the stream is the only finished signal.
func TestRun_EmitsTerminalStatusOnFailure(t *testing.T) {
	var mu sync.Mutex
	var terminal []pipeline.PipelineEvent

	res, err := Run(context.Background(), costDip, Config{
		Format:       "dip",
		WorkingDir:   t.TempDir(),
		LLMClient:    &failingCompleter{err: errors.New("provider exploded")},
		RetryPolicy:  "none",
		EventHandler: collectTerminal(&mu, &terminal),
	})
	if err != nil {
		t.Fatalf("Run returned an unexpected error: %v", err)
	}
	if res.Status != string(pipeline.OutcomeFail) {
		t.Fatalf("expected a failing terminal status, got %q", res.Status)
	}
	if len(terminal) != 1 {
		t.Fatalf("expected exactly one terminal-status event, got %d", len(terminal))
	}
	if got := terminal[0].TerminalStatus; got != string(pipeline.OutcomeFail) {
		t.Fatalf("TerminalStatus = %q, want %q", got, pipeline.OutcomeFail)
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
