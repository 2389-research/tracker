// ABOUTME: End-to-end tests that run a real pipeline and assert the activity log carries the run-reconstruction fields.
// ABOUTME: Covers the whole path — agent session -> event -> JSONL -> run.json — which unit tests per layer cannot.
package tracker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// toolCallingStub asks for one tool call, then answers with text so the session
// terminates. The shipped stubs never request a tool, which left tool_input,
// multi-turn, and turn-on-tool-events testable only against a live provider.
type toolCallingStub struct {
	calls int
}

func (s *toolCallingStub) Complete(_ context.Context, _ *llm.Request) (*llm.Response, error) {
	s.calls++
	if s.calls == 1 {
		return &llm.Response{
			Message: llm.Message{
				Role: llm.RoleAssistant,
				Content: []llm.ContentPart{{
					Kind: llm.KindToolCall,
					ToolCall: &llm.ToolCallData{
						ID:   "call_1",
						Name: "definitely_not_a_registered_tool",
						// Deliberately longer than the 80-char preview limit:
						// a clipped copy reaching the log would be invisible
						// without something long enough to notice.
						Arguments: json.RawMessage(`{"path":"` + strings.Repeat("z", 120) + `"}`),
					},
				}},
			},
			FinishReason: llm.FinishReason{Reason: "tool_calls"},
			Usage:        llm.Usage{InputTokens: 100, OutputTokens: 20, TotalTokens: 120},
		}, nil
	}
	return &llm.Response{
		Message:      llm.AssistantMessage("done"),
		FinishReason: llm.FinishReason{Reason: "stop"},
		Usage:        llm.Usage{InputTokens: 130, OutputTokens: 5, TotalTokens: 135},
	}, nil
}

// captureDip has agent nodes with prompts. A prompt is required: codergen
// skips the LLM entirely for a promptless agent node, so a pipeline without one
// produces no agent session and therefore none of the events under test.
const captureDip = `workflow capture
  start: A
  exit: B

  agent A
    label: A
    prompt:
      Do the first thing.

  agent B
    label: B
    prompt:
      Do the second thing.

  edges
    A -> B
`

// logEntry is the subset of the on-disk activity-log schema these tests read.
// Declared here rather than reaching into pipeline's unexported type, so the
// test asserts against the wire format a real consumer sees.
type logEntry struct {
	Source     string `json:"source"`
	Type       string `json:"type"`
	NodeID     string `json:"node_id"`
	NodeKind   string `json:"node_kind"`
	AttemptNo  int    `json:"attempt_no"`
	SessionID  string `json:"session_id"`
	TurnNo     int    `json:"turn_no"`
	ToolName   string `json:"tool_name"`
	ToolInput  string `json:"tool_input"`
	CallID     string `json:"call_id"`
	Terminal   string `json:"terminal_status"`
	Content    string `json:"content"`
	TokenIn    int    `json:"token_input"`
	TokenOut   int    `json:"token_output"`
	FinishWhy  string `json:"finish_reason"`
	TurnMs     int64  `json:"turn_duration_ms"`
	ToolCallMs int64  `json:"tool_duration_ms"`
}

// readRunLog returns the parsed activity log for the single run under workDir.
func readRunLog(t *testing.T, workDir string) []logEntry {
	t.Helper()
	runsDir := filepath.Join(workDir, ".tracker", "runs")
	dirs, err := os.ReadDir(runsDir)
	if err != nil {
		t.Fatalf("read runs dir: %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("expected exactly 1 run dir, got %d", len(dirs))
	}
	path := filepath.Join(runsDir, dirs[0].Name(), "activity.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read activity log: %v", err)
	}
	var out []logEntry
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimPrefix(line, pipeline.ActivityLogSentinel)
		if line == "" {
			continue
		}
		var e logEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}

// TestRunCapture_ActivityLogCarriesReconstructionFields runs a real pipeline and
// asserts the on-disk log carries everything needed to rebuild the run as a
// tree. Each field here was populated in memory and dropped at the log
// boundary; only an end-to-end run proves the whole chain.
func TestRunCapture_ActivityLogCarriesReconstructionFields(t *testing.T) {
	workDir := t.TempDir()
	handler := pipeline.NewJSONLEventHandler(filepath.Join(workDir, ".tracker", "runs"))
	stub := &toolCallingStub{}

	_, err := Run(context.Background(), captureDip, Config{
		Format:       "dip",
		WorkingDir:   workDir,
		LLMClient:    stub,
		EventHandler: handler,
		AgentEvents:  agent.EventHandlerFunc(handler.WriteAgentEvent),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := handler.Close(); err != nil {
		t.Fatalf("close log: %v", err)
	}

	entries := readRunLog(t, workDir)

	t.Run("every agent-source event carries a session id", func(t *testing.T) {
		// Without this, events from concurrently-executing parallel branches
		// cannot be separated: they interleave into one file.
		checked := 0
		for _, e := range entries {
			if e.Source != "agent" || !sessionScopedType(e.Type) {
				continue
			}
			checked++
			if e.SessionID == "" {
				t.Errorf("%s carries no session_id", e.Type)
			}
		}
		if checked == 0 {
			t.Fatal("no session-scoped agent events in the log")
		}
	})

	t.Run("tool events carry the turn that issued them and the full input", func(t *testing.T) {
		var found bool
		for _, e := range entries {
			if e.Type != "tool_call_start" {
				continue
			}
			found = true
			if e.TurnNo != 1 {
				t.Errorf("tool_call_start turn_no = %d, want 1", e.TurnNo)
			}
			if e.ToolName == "" {
				t.Error("tool_call_start carries no tool_name")
			}
			// The arguments are 120+ chars; a clipped copy would prove the
			// preview path is still in play.
			if !strings.Contains(e.ToolInput, strings.Repeat("z", 120)) {
				t.Errorf("tool_input is truncated or absent: %q", e.ToolInput)
			}
		}
		if !found {
			t.Fatal("no tool_call_start event reached the log")
		}
	})

	t.Run("the run reached a second turn", func(t *testing.T) {
		// A tool call means the session loops; turn 2 is what proves the turn
		// counter is real rather than hardcoded.
		maxTurn := 0
		for _, e := range entries {
			if e.TurnNo > maxTurn {
				maxTurn = e.TurnNo
			}
		}
		if maxTurn < 2 {
			t.Errorf("highest turn_no = %d, want >= 2", maxTurn)
		}
	})

	t.Run("node events carry kind and attempt", func(t *testing.T) {
		var found bool
		for _, e := range entries {
			if e.Type != "stage_started" {
				continue
			}
			found = true
			if e.NodeKind == "" {
				t.Errorf("stage_started for %q carries no node_kind", e.NodeID)
			}
			if e.AttemptNo != 1 {
				t.Errorf("stage_started for %q attempt_no = %d, want 1", e.NodeID, e.AttemptNo)
			}
		}
		if !found {
			t.Fatal("no stage_started event reached the log")
		}
	})

	t.Run("exactly one event carries the terminal status", func(t *testing.T) {
		var statuses []string
		for _, e := range entries {
			if e.Terminal != "" {
				statuses = append(statuses, e.Terminal)
			}
		}
		if len(statuses) != 1 {
			t.Fatalf("terminal_status appears %d times %v, want exactly 1", len(statuses), statuses)
		}
		if statuses[0] != "success" {
			t.Errorf("terminal_status = %q, want success", statuses[0])
		}
	})

	t.Run("llm events carry a call id and the wire request body", func(t *testing.T) {
		// Structurally out of reach here: a bare agent.Completer stub bypasses
		// llm.Client, so completeWithTrace never runs, no CallID is minted, and
		// no llm_* trace events exist to carry the wire body. Only a run through
		// a real client and provider adapter exercises that path — which is
		// exactly why a live Haiku run was the first thing to catch these being
		// dropped at the agent re-emission boundary, and why asserting it here
		// would test the harness rather than the code. Covered instead by
		// pipeline.TestApplyAgentEventFieldsCarriesCallIDAndRequestRaw (the
		// boundary itself) and by capture-tests/t2 (the whole path, live).
		var sawLLM bool
		for _, e := range entries {
			if e.Type == "llm_request_start" || e.Type == "llm_finish" {
				sawLLM = true
			}
		}
		if !sawLLM {
			t.Skip("no llm_* trace events: stub Completer bypasses llm.Client")
		}
		var sawCallID, sawBody bool
		for _, e := range entries {
			if e.CallID != "" {
				sawCallID = true
			}
			if e.Type == "llm_request_start" && strings.HasPrefix(strings.TrimSpace(e.Content), "{") {
				sawBody = true
			}
		}
		if !sawCallID {
			t.Error("no event carries a call_id — usage cannot be de-duplicated across log paths")
		}
		if !sawBody {
			t.Error("no llm_request_start carries the wire request body")
		}
	})

	t.Run("per-turn economics reach the log", func(t *testing.T) {
		var found bool
		for _, e := range entries {
			if e.Type != "turn_metrics" {
				continue
			}
			found = true
			if e.TokenIn == 0 || e.TokenOut == 0 {
				t.Errorf("turn_metrics has no token counts (in=%d out=%d)", e.TokenIn, e.TokenOut)
			}
		}
		if !found {
			t.Fatal("no turn_metrics event reached the log")
		}
	})
}

// TestRunCapture_ManifestAssemblesFromRealRun closes the loop: the manifest is
// built from the log a real run produced, not from a synthetic one.
func TestRunCapture_ManifestAssemblesFromRealRun(t *testing.T) {
	workDir := t.TempDir()
	runsDir := filepath.Join(workDir, ".tracker", "runs")
	handler := pipeline.NewJSONLEventHandler(runsDir)

	result, err := Run(context.Background(), captureDip, Config{
		Format:       "dip",
		WorkingDir:   workDir,
		LLMClient:    &toolCallingStub{},
		EventHandler: handler,
		AgentEvents:  agent.EventHandlerFunc(handler.WriteAgentEvent),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := handler.Close(); err != nil {
		t.Fatalf("close log: %v", err)
	}

	runDir := filepath.Join(runsDir, result.RunID)
	m, err := pipeline.AssembleRunManifest(runDir, result.RunID)
	if err != nil {
		t.Fatalf("AssembleRunManifest: %v", err)
	}

	if m.TerminalStatus != "success" {
		t.Errorf("terminal_status = %q, want success", m.TerminalStatus)
	}
	// quickDip has two agent nodes; both must appear, with their kind.
	if len(m.Nodes) != 2 {
		t.Fatalf("got %d nodes, want 2: %+v", len(m.Nodes), m.Nodes)
	}
	for _, n := range m.Nodes {
		if n.Kind == "" {
			t.Errorf("node %q has no kind", n.ID)
		}
		if n.Turns == 0 {
			t.Errorf("node %q recorded no turns despite running an agent", n.ID)
		}
	}
	if m.Totals.InputTokens == 0 {
		t.Error("totals record no input tokens despite two agent nodes running")
	}
	if len(m.ToolCalls) == 0 {
		t.Error("tool histogram is empty despite the stub requesting a tool")
	}
}

// sessionScopedType reports whether an event type is emitted from inside an
// agent session and must therefore carry a session id.
func sessionScopedType(t string) bool {
	switch t {
	case "session_start", "session_end", "turn_start", "turn_end",
		"tool_call_start", "tool_call_end", "turn_metrics":
		return true
	}
	return false
}
