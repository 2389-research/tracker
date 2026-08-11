// ABOUTME: Round-trip / replay-fidelity conformance test for the OpenAI Responses API request translation.
// ABOUTME: Guards the omitempty-on-required-field bug class (#567/#568/#114) — required keys of replayable input items must survive serialization even when empty.
package openai

import (
	"encoding/json"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// The bug class under test: a translate field with `omitempty` on a value type,
// where the provider treats *present-but-empty* as different from *absent*,
// silently corrupts the wire. #567 (Anthropic thinking.thinking), #568 (Anthropic
// tool_use.input), and #114 (OpenAI function_call_output.output) are the live
// instances.
//
// The invariant: for content blocks the provider REQUIRES you to replay into the
// next request (assistant text, function_call, tool result / function_call_output),
// our request serialization must preserve every schema-required field — INCLUDING
// present-but-empty ones. The check is "every required-present key of a replayable
// item survives parse -> re-serialize", NOT "response bytes == request bytes".
//
// For OpenAI the replayable items live in the flat `input` array of POST
// /v1/responses. openaiInput.MarshalJSON (translate.go) is deliberately written
// WITHOUT omitempty on required fields precisely so this class of bug cannot
// occur; this test freezes that contract as a CI gate.

// requiredKeysFor maps the discriminator ("type" for typed items, "" for
// role-based messages) to the set of keys the OpenAI Responses API requires to
// be present even when their value is empty.
//
// Conservative by design — only keys the API genuinely requires present-when-empty
// are listed, to avoid false-positive CI failures:
//   - role message: role + content (a bare assistant/user turn).
//   - function_call: type + call_id + name + arguments. A no-argument tool call is
//     the #568 analog — arguments must be PRESENT (not dropped by omitempty).
//   - function_call_output: type + call_id + output. Empty tool output is the #114
//     concern — output must be PRESENT even when the tool returned "".
var requiredKeysFor = map[string][]string{
	"":                     {"role", "content"},
	"function_call":        {"type", "call_id", "name", "arguments"},
	"function_call_output": {"type", "call_id", "output"},
}

// decodeInputItems runs a request through the ACTUAL request-translation entry
// point (translateRequest) and returns each `input` array item decoded as a raw
// key map. Presence of a key in the map is the exact test the omitempty bug
// class needs: omitempty DROPS the key entirely, so `_, ok := item[key]` is a
// faithful present/absent probe.
func decodeInputItems(t *testing.T, msgs []llm.Message) []map[string]json.RawMessage {
	t.Helper()
	req := llm.Request{Model: "gpt-x", Messages: msgs}
	body, err := translateRequest(&req)
	if err != nil {
		t.Fatalf("translateRequest: %v", err)
	}
	var wire struct {
		Input []json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	items := make([]map[string]json.RawMessage, 0, len(wire.Input))
	for _, raw := range wire.Input {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("unmarshal input item: %v", err)
		}
		items = append(items, m)
	}
	return items
}

// itemDiscriminator returns the OpenAI item discriminator: the "type" field for
// typed items, or "" for a role-based message (which has no "type" key).
func itemDiscriminator(t *testing.T, item map[string]json.RawMessage) string {
	t.Helper()
	raw, ok := item["type"]
	if !ok {
		return ""
	}
	var typ string
	if err := json.Unmarshal(raw, &typ); err != nil {
		t.Fatalf("unmarshal type: %v", err)
	}
	return typ
}

// assertRequiredPresent fails if any schema-required key for the item's
// discriminator is absent. Empty VALUE is fine — presence is the invariant.
func assertRequiredPresent(t *testing.T, item map[string]json.RawMessage) {
	t.Helper()
	disc := itemDiscriminator(t, item)
	keys, known := requiredKeysFor[disc]
	if !known {
		t.Fatalf("unexpected input item discriminator %q (item=%v)", disc, item)
	}
	for _, k := range keys {
		if _, ok := item[k]; !ok {
			t.Errorf("required key %q MISSING from %q item — omitempty likely dropped a present-but-empty field (bug class #567/#568/#114); item=%v", k, disc, item)
		}
	}
}

// TestConformanceReplayRequiredFieldsPresent is the table-driven core: for every
// replayable block type it builds a NORMAL and an EDGE (empty-but-required-present)
// message, serializes through translateRequest, and asserts every schema-required
// key of the emitted input item is present.
func TestConformanceReplayRequiredFieldsPresent(t *testing.T) {
	cases := []struct {
		name     string
		msgs     []llm.Message
		wantType string // discriminator of the item we assert on
	}{
		{
			name:     "assistant_text_normal",
			msgs:     []llm.Message{{Role: llm.RoleAssistant, Content: []llm.ContentPart{{Kind: llm.KindText, Text: "hello"}}}},
			wantType: "",
		},
		{
			// Empty assistant text is still present-but-empty: role + content keys
			// must survive. (translateAssistantInput only emits a text item when
			// at least one text part exists, so we use a single empty-string part.)
			name:     "assistant_text_empty",
			msgs:     []llm.Message{{Role: llm.RoleAssistant, Content: []llm.ContentPart{{Kind: llm.KindText, Text: ""}}}},
			wantType: "",
		},
		{
			name:     "user_text_normal",
			msgs:     []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentPart{{Kind: llm.KindText, Text: "hi"}}}},
			wantType: "",
		},
		{
			name: "function_call_normal",
			msgs: []llm.Message{{Role: llm.RoleAssistant, Content: []llm.ContentPart{{
				Kind:     llm.KindToolCall,
				ToolCall: &llm.ToolCallData{ID: "call_1", Name: "get_weather", Arguments: json.RawMessage(`{"city":"SF"}`)},
			}}}},
			wantType: "function_call",
		},
		{
			// #568 analog: a no-argument tool call. Arguments is empty; the
			// `arguments` key MUST still be present on the wire (not dropped).
			name: "function_call_empty_args",
			msgs: []llm.Message{{Role: llm.RoleAssistant, Content: []llm.ContentPart{{
				Kind:     llm.KindToolCall,
				ToolCall: &llm.ToolCallData{ID: "call_2", Name: "noargs", Arguments: nil},
			}}}},
			wantType: "function_call",
		},
		{
			name: "function_call_output_normal",
			msgs: []llm.Message{{Role: llm.RoleTool, ToolCallID: "call_1", Content: []llm.ContentPart{{
				Kind:       llm.KindToolResult,
				ToolResult: &llm.ToolResultData{ToolCallID: "call_1", Content: "sunny"},
			}}}},
			wantType: "function_call_output",
		},
		{
			// #114: a tool that returned an empty string. `output` must be present.
			name: "function_call_output_empty",
			msgs: []llm.Message{{Role: llm.RoleTool, ToolCallID: "call_1", Content: []llm.ContentPart{{
				Kind:       llm.KindToolResult,
				ToolResult: &llm.ToolResultData{ToolCallID: "call_1", Content: ""},
			}}}},
			wantType: "function_call_output",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items := decodeInputItems(t, tc.msgs)
			var found bool
			for _, item := range items {
				if itemDiscriminator(t, item) == tc.wantType {
					found = true
					assertRequiredPresent(t, item)
				}
			}
			if !found {
				t.Fatalf("no input item with discriminator %q emitted; got %d items: %v", tc.wantType, len(items), items)
			}
		})
	}
}

// TestConformanceEmptyArgsArgumentsPresent pins the #568-mirror explicitly: a
// no-argument function_call keeps the `arguments` key present. The CURRENT
// contract serializes it as the empty string "" (documented here — OpenAI's
// stricter validators would prefer "{}", but presence, not value, is the
// omitempty invariant this suite guards). If a maintainer ever adds omitempty
// to that field this assertion — and the table case above — go red.
func TestConformanceEmptyArgsArgumentsPresent(t *testing.T) {
	msgs := []llm.Message{{Role: llm.RoleAssistant, Content: []llm.ContentPart{{
		Kind:     llm.KindToolCall,
		ToolCall: &llm.ToolCallData{ID: "call_2", Name: "noargs", Arguments: nil},
	}}}}
	items := decodeInputItems(t, msgs)
	if len(items) != 1 {
		t.Fatalf("want 1 input item, got %d: %v", len(items), items)
	}
	raw, ok := items[0]["arguments"]
	if !ok {
		t.Fatalf("arguments key dropped for no-arg function_call (#568 class regression)")
	}
	var args string
	if err := json.Unmarshal(raw, &args); err != nil {
		t.Fatalf("arguments not a JSON string: %v", err)
	}
	// Document the current serialized shape without failing on it: presence is
	// the guarded invariant; "" is what translate.go emits today.
	t.Logf("current no-arg function_call arguments serialization = %q (present, not dropped)", args)
}

// TestConformanceReasoningNotReplayed encodes the KNOWN, INTENTIONAL contract
// gap as an explicit assertion rather than a silent hole: translateAssistantInput
// only handles KindText and KindToolCall, so an assistant KindThinking part
// produces NO input item. OpenAI reasoning is opaque and server-side; tracker
// does not round-trip it into the request. This is deliberate — the assertion
// exists so that if a maintainer starts replaying reasoning, they revisit its
// required-field fidelity here on purpose.
func TestConformanceReasoningNotReplayed(t *testing.T) {
	msgs := []llm.Message{{Role: llm.RoleAssistant, Content: []llm.ContentPart{{
		Kind:     llm.KindThinking,
		Thinking: &llm.ThinkingData{Text: "let me think", Signature: "sig-abc"},
	}}}}
	items := decodeInputItems(t, msgs)
	if len(items) != 0 {
		t.Fatalf("expected reasoning to NOT be replayed into the request (current contract), but got %d input item(s): %v", len(items), items)
	}
}

// TestConformanceStreamAccumulatorToolCallReplay exercises the #568 path the way
// the concern actually arises for tool calls: the value reaches the request via
// the STREAM accumulator, not response-parse. A no-argument tool call arrives as
// EventToolCallStart (no args, no delta) + EventToolCallEnd; stream.go leaves
// Arguments == "" (an empty json.RawMessage). We take the accumulator's Response,
// replay its assistant tool-call part through translateRequest, and assert the
// `arguments` key (and the rest of the function_call required set) survives.
func TestConformanceStreamAccumulatorToolCallReplay(t *testing.T) {
	acc := llm.NewStreamAccumulator()
	acc.Process(llm.StreamEvent{
		Type:     llm.EventToolCallStart,
		ToolCall: &llm.ToolCallData{ID: "call_9", Name: "noargs"},
	})
	acc.Process(llm.StreamEvent{Type: llm.EventToolCallEnd})
	resp := acc.Response()

	// The accumulated assistant message is what a follow-up turn replays.
	items := decodeInputItems(t, []llm.Message{resp.Message})
	var found bool
	for _, item := range items {
		if itemDiscriminator(t, item) == "function_call" {
			found = true
			assertRequiredPresent(t, item)
		}
	}
	if !found {
		t.Fatalf("stream-accumulated tool call did not serialize to a function_call input item; items=%v", items)
	}
}

// TestConformanceStreamAccumulatorTextReplay covers the stream path for a text
// block: text deltas accumulate into an assistant message that a follow-up turn
// replays; role + content must survive serialization.
func TestConformanceStreamAccumulatorTextReplay(t *testing.T) {
	acc := llm.NewStreamAccumulator()
	acc.Process(llm.StreamEvent{Type: llm.EventTextStart, TextID: "t1"})
	acc.Process(llm.StreamEvent{Type: llm.EventTextDelta, TextID: "t1", Delta: "streamed answer"})
	resp := acc.Response()

	items := decodeInputItems(t, []llm.Message{resp.Message})
	var found bool
	for _, item := range items {
		if itemDiscriminator(t, item) == "" {
			found = true
			assertRequiredPresent(t, item)
		}
	}
	if !found {
		t.Fatalf("stream-accumulated text did not serialize to a role message input item; items=%v", items)
	}
}
