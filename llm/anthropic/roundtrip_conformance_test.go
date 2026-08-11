// ABOUTME: Replay-fidelity conformance test for Anthropic request serialization.
// ABOUTME: Guards that schema-REQUIRED keys of replayable content blocks survive parse -> re-serialize, even when present-but-empty.
package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// The invariant under test: for content blocks the Anthropic Messages API
// REQUIRES you to replay on the next turn (assistant thinking / redacted_thinking
// / tool_use blocks in a tool-use continuation), our request serialization must
// preserve every schema-required field — INCLUDING present-but-empty ones.
//
// This is NOT "response bytes == request bytes". It is: every required-present
// field of a replayable block survives parse -> re-serialize. The failure mode
// it catches is a struct field with `omitempty` on a VALUE type where Anthropic
// treats present-but-empty as DIFFERENT from absent:
//   - #567 (FIXED): thinking block with empty "thinking" + a signature. omitempty
//     dropped the empty string -> 400 "thinking.thinking: Field required".
//   - #568 (OPEN): tool_use with no arguments reaching the request via the stream
//     accumulator (Arguments == "") -> omitempty drops "input" -> 400
//     "input: Field required".

// replayableContentBlocks serializes one assistant message through the ACTUAL
// request-translation entry point (translateRequest) and returns the decoded
// content blocks of that message as raw key maps, so presence/absence of each
// schema key is directly observable.
func replayableContentBlocks(t *testing.T, msg llm.Message) []map[string]json.RawMessage {
	t.Helper()
	body, err := translateRequest(&llm.Request{
		Model:    "claude-x",
		Messages: []llm.Message{msg},
	})
	if err != nil {
		t.Fatalf("translateRequest failed: %v", err)
	}
	var wire struct {
		Messages []struct {
			Role    string                       `json:"role"`
			Content []map[string]json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
	}
	if len(wire.Messages) != 1 {
		t.Fatalf("message count = %d, want 1; body: %s", len(wire.Messages), body)
	}
	if len(wire.Messages[0].Content) == 0 {
		t.Fatalf("message has no content blocks; body: %s", body)
	}
	return wire.Messages[0].Content
}

// parseResponseMessage runs an Anthropic response body through the ACTUAL
// response-parse entry point (translateResponse) and returns the assistant
// message — the value that would be replayed into the next request.
func parseResponseMessage(t *testing.T, respJSON string) llm.Message {
	t.Helper()
	resp, err := translateResponse([]byte(respJSON))
	if err != nil {
		t.Fatalf("translateResponse failed: %v", err)
	}
	return resp.Message
}

func responseEnvelope(contentBlock string) string {
	return `{"id":"msg_1","type":"message","role":"assistant","model":"claude-x",` +
		`"content":[` + contentBlock + `],"stop_reason":"end_turn",` +
		`"usage":{"input_tokens":1,"output_tokens":1}}`
}

func TestConformanceReplayFidelity(t *testing.T) {
	cases := []struct {
		name string
		// build yields the assistant message to replay, exercising the real
		// parse path (response-parse or stream-accumulator).
		build func(t *testing.T) llm.Message
		// wantType is the expected content-block "type" of the first block.
		wantType string
		// presentKeys must be present (empty value is fine — presence is the point).
		presentKeys []string
		// absentKeys must NOT be present.
		absentKeys []string
		// knownBug, if set, converts an absent required key into t.Skip(knownBug)
		// instead of a failure — for a case that fails on current main due to an
		// open bug. When the bug is fixed the case activates automatically.
		knownBug string
	}{
		{
			name: "text",
			build: func(t *testing.T) llm.Message {
				return parseResponseMessage(t, responseEnvelope(
					`{"type":"text","text":"hello"}`))
			},
			wantType:    "text",
			presentKeys: []string{"type", "text"},
		},
		{
			name: "thinking_nonempty",
			build: func(t *testing.T) llm.Message {
				return parseResponseMessage(t, responseEnvelope(
					`{"type":"thinking","thinking":"let me reason","signature":"sig-abc"}`))
			},
			wantType:    "thinking",
			presentKeys: []string{"type", "thinking", "signature"},
		},
		{
			// #567: Sonnet 5 / Opus 5 / Fable 5 return an empty-text thinking
			// block bearing only a signature. Anthropic requires the "thinking"
			// key be present on replay even though it is the empty string.
			// Guards the shipped *string fix in anthropicContent.Thinking.
			name: "thinking_empty_signature_only",
			build: func(t *testing.T) llm.Message {
				return parseResponseMessage(t, responseEnvelope(
					`{"type":"thinking","thinking":"","signature":"sig-only"}`))
			},
			wantType:    "thinking",
			presentKeys: []string{"type", "thinking", "signature"},
		},
		{
			// redacted_thinking carries an opaque "data" blob and NO "thinking"
			// key. Replay must preserve "data" and must not emit "thinking".
			name: "redacted_thinking",
			build: func(t *testing.T) llm.Message {
				return parseResponseMessage(t, responseEnvelope(
					`{"type":"redacted_thinking","data":"redacted-blob"}`))
			},
			wantType:    "redacted_thinking",
			presentKeys: []string{"type", "data"},
			absentKeys:  []string{"thinking"},
		},
		{
			name: "tool_use_with_args",
			build: func(t *testing.T) llm.Message {
				return parseResponseMessage(t, responseEnvelope(
					`{"type":"tool_use","id":"tid-1","name":"edit","input":{"path":"a.txt"}}`))
			},
			wantType:    "tool_use",
			presentKeys: []string{"type", "id", "name", "input"},
		},
		{
			// A no-argument tool's valid wire shape is "input":{}. Via the
			// response-parse path Anthropic sends the literal {}, so Arguments is
			// non-empty and "input" survives. This passes on current main.
			name: "tool_use_no_args_response_path",
			build: func(t *testing.T) llm.Message {
				return parseResponseMessage(t, responseEnvelope(
					`{"type":"tool_use","id":"tid-2","name":"now","input":{}}`))
			},
			wantType:    "tool_use",
			presentKeys: []string{"type", "id", "name", "input"},
		},
		{
			// #568: the SAME no-argument tool reaching the request via the STREAM
			// accumulator. With no argument deltas, processToolCallEnd sets
			// Arguments = json.RawMessage("") (empty), and `omitempty` on the
			// tool_use Input field drops "input" entirely -> Anthropic 400
			// "input: Field required" on replay. Fails on current main; skips
			// via knownBug until the accumulator or translate is fixed.
			name: "tool_use_no_args_stream_path",
			build: func(t *testing.T) llm.Message {
				acc := llm.NewStreamAccumulator()
				acc.Process(llm.StreamEvent{
					Type:     llm.EventToolCallStart,
					ToolCall: &llm.ToolCallData{ID: "tid-3", Name: "now"},
				})
				// No EventToolCallDelta — a genuine no-argument call.
				acc.Process(llm.StreamEvent{Type: llm.EventToolCallEnd})
				return acc.Response().Message
			},
			wantType:    "tool_use",
			presentKeys: []string{"type", "id", "name", "input"},
			// #568 FIXED: StreamAccumulator.processToolCallEnd normalizes an empty
			// no-arg tool call to "{}", so the replayed tool_use carries input:{}.
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blocks := replayableContentBlocks(t, tc.build(t))
			block := blocks[0]

			gotType := ""
			if raw, ok := block["type"]; ok {
				_ = json.Unmarshal(raw, &gotType)
			}
			if gotType != tc.wantType {
				t.Fatalf("first block type = %q, want %q; block: %v", gotType, tc.wantType, block)
			}

			for _, key := range tc.presentKeys {
				if _, ok := block[key]; !ok {
					if tc.knownBug != "" {
						t.Skip(tc.knownBug)
					}
					t.Errorf("required-present key %q is MISSING from replayed %s block; keys present: %v",
						key, tc.wantType, keysOf(block))
				}
			}

			for _, key := range tc.absentKeys {
				if _, ok := block[key]; ok {
					t.Errorf("key %q must be ABSENT from replayed %s block but is present",
						key, tc.wantType)
				}
			}
		})
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
