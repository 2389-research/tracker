// ABOUTME: Tests for sub-node turn checkpointing (#427): snapshot persistence,
// ABOUTME: per-provider message fidelity, and interrupt-and-resume mid-node.
package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// cancelOnNthTool is a stub tool that cancels a captured context on its Nth
// Execute call, simulating a mid-node interruption (crash / SIGTERM) that leaves
// a durable turn snapshot behind. The cancellation only takes effect at the top
// of the NEXT turn, so the turn that triggered it still completes and persists.
type cancelOnNthTool struct {
	name   string
	n      int
	calls  int
	cancel context.CancelFunc
}

func (t *cancelOnNthTool) Name() string        { return t.name }
func (t *cancelOnNthTool) Description() string { return "cancels context on Nth call" }
func (t *cancelOnNthTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

func (t *cancelOnNthTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	t.calls++
	if t.calls == t.n {
		t.cancel()
	}
	return "ok", nil
}

// toolCallResponse builds an assistant response that calls the named tool once.
func toolCallResponse(id, name string) *llm.Response {
	return &llm.Response{
		Message: llm.Message{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{{
				Kind:     llm.KindToolCall,
				ToolCall: &llm.ToolCallData{ID: id, Name: name, Arguments: json.RawMessage(`{}`)},
			}},
		},
		FinishReason: llm.FinishReason{Reason: "tool_calls"},
		Usage:        llm.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}
}

// TestTurnSnapshot_SaveLoadRoundTrip is the durable-persistence contract: a
// snapshot saved atomically reloads byte-identically.
func TestTurnSnapshot_SaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "Build.json")
	snap := &TurnSnapshot{
		Schema:    turnSnapshotSchema,
		SessionID: "abcd1234",
		Turn:      3,
		Messages: []llm.Message{
			llm.SystemMessage("you are a coding agent"),
			llm.UserMessage("do the thing"),
		},
		Episodes: []EpisodeEntry{{Tool: "read", Args: "{}", Success: true, Summary: "ok"}},
	}
	if err := snap.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := LoadTurnSnapshot(path)
	if err != nil {
		t.Fatalf("LoadTurnSnapshot: %v", err)
	}
	if !reflect.DeepEqual(got, snap) {
		t.Errorf("round-trip mismatch:\n got=%+v\nwant=%+v", got, snap)
	}
}

// TestLoadTurnSnapshot_MissingIsNotError pins that "no snapshot yet" is the
// common, non-error path (first run of a node).
func TestLoadTurnSnapshot_MissingIsNotError(t *testing.T) {
	got, err := LoadTurnSnapshot(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("missing snapshot should not error, got: %v", err)
	}
	if got != nil {
		t.Errorf("missing snapshot should return nil, got: %+v", got)
	}
}

// TestLoadTurnSnapshot_UnknownSchemaErrors pins that a stale/unknown schema is
// surfaced, never silently mis-decoded.
func TestLoadTurnSnapshot_UnknownSchemaErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte(`{"schema":999,"turn":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTurnSnapshot(path); err == nil {
		t.Error("expected error for unknown schema version")
	}
}

// TestTurnSnapshot_PerProviderMessageFidelity is the deepest-risk check called
// out in #427: llm.Message (including tool-call and tool-result parts, thinking
// with signatures, and provider-specific fields like thought_signature and image
// bytes) must round-trip through the snapshot's JSON serialization for every
// provider shape. A silent field drop here would corrupt a resumed conversation.
func TestTurnSnapshot_PerProviderMessageFidelity(t *testing.T) {
	cases := map[string][]llm.Message{
		"anthropic": {
			{Role: llm.RoleAssistant, Content: []llm.ContentPart{
				{Kind: llm.KindThinking, Thinking: &llm.ThinkingData{Text: "let me think", Signature: "sig-abc"}},
				{Kind: llm.KindToolCall, ToolCall: &llm.ToolCallData{ID: "toolu_1", Name: "edit", Arguments: json.RawMessage(`{"path":"a.go","old":"x","new":"y"}`)}},
			}},
			llm.ToolResultMessage("toolu_1", "edited", false),
		},
		"openai": {
			{Role: llm.RoleAssistant, Content: []llm.ContentPart{
				{Kind: llm.KindToolCall, ToolCall: &llm.ToolCallData{ID: "call_9", Name: "read", Arguments: json.RawMessage(`{"path":"b.go"}`)}},
			}},
			{Role: llm.RoleTool, ToolCallID: "call_9", Content: []llm.ContentPart{
				{Kind: llm.KindToolResult, ToolResult: &llm.ToolResultData{ToolCallID: "call_9", Name: "read", Content: "package main", IsError: false}},
			}},
		},
		"gemini": {
			{Role: llm.RoleAssistant, Content: []llm.ContentPart{
				{Kind: llm.KindToolCall, ToolCall: &llm.ToolCallData{ID: "fc_2", Name: "run", Arguments: json.RawMessage(`{"cmd":"go build"}`), ThoughtSigData: "thought-xyz"}},
			}},
			{Role: llm.RoleTool, Content: []llm.ContentPart{
				{Kind: llm.KindToolResult, ToolResult: &llm.ToolResultData{ToolCallID: "fc_2", Content: "built", ImageData: []byte{0x01, 0x02, 0x03}, ImageMediaType: "image/png"}},
			}},
		},
	}
	for provider, msgs := range cases {
		t.Run(provider, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "s.json")
			snap := &TurnSnapshot{Schema: turnSnapshotSchema, SessionID: "id", Turn: 1, Messages: msgs}
			if err := snap.Save(path); err != nil {
				t.Fatalf("Save: %v", err)
			}
			got, err := LoadTurnSnapshot(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if !reflect.DeepEqual(got.Messages, msgs) {
				t.Errorf("%s message fidelity lost:\n got=%#v\nwant=%#v", provider, got.Messages, msgs)
			}
		})
	}
}

// TestTurnSnapshot_WorkTreeGuard pins the corruption safety valve: an empty
// captured SHA never vetoes a resume (guard unarmed), while a captured SHA that
// no longer matches the current tree does.
func TestTurnSnapshot_WorkTreeGuard(t *testing.T) {
	if (&TurnSnapshot{}).WorkTreeMatches("/nonexistent") == false {
		t.Error("empty captured SHA must match unconditionally (guard unarmed)")
	}
	// A repo-less temp dir yields an empty live SHA, so a non-empty captured SHA
	// cannot match — the guard vetoes resume onto a tree it can't confirm.
	if (&TurnSnapshot{WorkTreeSHA: "deadbeef"}).WorkTreeMatches(t.TempDir()) {
		t.Error("non-empty captured SHA must not match a repo-less tree")
	}
}

// TestSession_RestoreFrom pins the in-memory restore contract: a fresh Session
// restored from a snapshot has messages and episode log byte-identical to the
// snapshot and resumes at Turn+1.
func TestSession_RestoreFrom(t *testing.T) {
	snap := &TurnSnapshot{
		Schema:    turnSnapshotSchema,
		SessionID: "restore-id",
		Turn:      4,
		Messages:  []llm.Message{llm.SystemMessage("sys"), llm.UserMessage("go")},
		Episodes:  []EpisodeEntry{{Tool: "read", Success: true, Summary: "ok"}},
	}
	sess := mustNewSession(t, &mockCompleter{}, DefaultConfig())
	sess.RestoreFrom(snap)

	if !reflect.DeepEqual(sess.messages, snap.Messages) {
		t.Errorf("messages not restored byte-identically:\n got=%#v\nwant=%#v", sess.messages, snap.Messages)
	}
	if !reflect.DeepEqual(sess.episodeLog.Entries, snap.Episodes) {
		t.Errorf("episode log not restored: got=%#v want=%#v", sess.episodeLog.Entries, snap.Episodes)
	}
	if sess.resumeTurn != 4 {
		t.Errorf("resumeTurn = %d, want 4", sess.resumeTurn)
	}
	if sess.id != "restore-id" {
		t.Errorf("session id = %q, want restore-id", sess.id)
	}
}

// TestSession_TurnCheckpointDefaultOff pins that with no TurnCheckpointPath the
// feature is a complete no-op: no snapshot file is written anywhere.
func TestSession_TurnCheckpointDefaultOff(t *testing.T) {
	dir := t.TempDir()
	client := &mockCompleter{responses: []*llm.Response{
		{Message: llm.AssistantMessage("done"), FinishReason: llm.FinishReason{Reason: "stop"}},
	}}
	cfg := DefaultConfig()
	cfg.WorkingDir = dir
	sess := mustNewSession(t, client, cfg)
	if _, err := sess.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("default-off run wrote files: %v", entries)
	}
}

// TestSession_ResumesMidNodeAfterInterrupt is the AC3 acceptance test: a session
// runs several turns, is interrupted mid-node (leaving a durable snapshot), and a
// fresh session auto-restores from that snapshot and resumes at turn N+1 with
// conversational state intact.
func TestSession_ResumesMidNodeAfterInterrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs", "run1", "turns", "Build.json")

	ctx, cancel := context.WithCancel(context.Background())
	interruptTool := &cancelOnNthTool{name: "work", n: 2, cancel: cancel}

	// Each turn calls the tool; the 2nd tool call cancels the context, so turn 3
	// aborts at the top of the loop — a mid-node interruption after turn 2.
	client1 := &mockCompleter{responses: []*llm.Response{
		toolCallResponse("c1", "work"),
		toolCallResponse("c2", "work"),
		toolCallResponse("c3", "work"),
	}}
	cfg := DefaultConfig()
	cfg.MaxTurns = 10
	cfg.TurnCheckpointPath = path
	sess1 := mustNewSession(t, client1, cfg, WithTools(interruptTool))

	if _, err := sess1.Run(ctx, "build it"); err == nil {
		t.Fatal("expected interruption error from cancelled context")
	}

	// The interruption must have left a durable snapshot behind.
	snap, err := LoadTurnSnapshot(path)
	if err != nil || snap == nil {
		t.Fatalf("expected a persisted snapshot after interrupt, got snap=%v err=%v", snap, err)
	}
	if snap.Turn != 2 {
		t.Fatalf("snapshot Turn = %d, want 2", snap.Turn)
	}
	// Pre-interrupt in-memory state must equal what was persisted.
	if !reflect.DeepEqual(sess1.messages, snap.Messages) {
		t.Errorf("persisted messages differ from live pre-interrupt messages")
	}
	if len(snap.Episodes) != 2 {
		t.Errorf("expected 2 tool episodes persisted, got %d", len(snap.Episodes))
	}

	// Resume: a fresh session auto-restores from the same path. Its first LLM
	// request must carry exactly the restored history (byte-identical), proving
	// the resume re-enters mid-node rather than restarting from scratch.
	var firstResumeRequest []llm.Message
	client2 := &mockCompleter{
		responses: []*llm.Response{
			{Message: llm.AssistantMessage("all done"), FinishReason: llm.FinishReason{Reason: "stop"}},
		},
		onComplete: func(req *llm.Request) {
			if firstResumeRequest == nil {
				firstResumeRequest = append([]llm.Message(nil), req.Messages...)
			}
		},
	}
	sess2 := mustNewSession(t, client2, cfg, WithTools(&stubTool{name: "work", output: "ok"}))
	result, err := sess2.Run(context.Background(), "build it")
	if err != nil {
		t.Fatalf("resume Run: %v", err)
	}
	if result.Turns != snap.Turn+1 {
		t.Errorf("resumed at turn %d, want %d (N+1)", result.Turns, snap.Turn+1)
	}
	// The restored history sent on the first resume turn must be byte-identical
	// to the pre-interrupt snapshot messages.
	wantJSON, _ := json.Marshal(snap.Messages)
	gotJSON, _ := json.Marshal(firstResumeRequest)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("resume did not send byte-identical restored history:\n got=%s\nwant=%s", gotJSON, wantJSON)
	}
	// Natural completion clears the snapshot so a later loop-restart starts fresh.
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("snapshot should be cleared after natural completion, stat err=%v", statErr)
	}
}
