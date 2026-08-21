// ABOUTME: Replay-fidelity conformance test for Google Gemini request serialization.
// ABOUTME: Guards that schema-REQUIRED keys of replayable Gemini parts survive parse -> re-serialize, even when present-but-empty.
package google

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// The invariant under test: for content parts the Gemini generateContent API
// REQUIRES you to replay on the next turn (assistant functionCall parts, and the
// Gemini-3 thoughtSignature that carries reasoning continuity), our request
// serialization must preserve every schema-required field — INCLUDING
// present-but-empty ones.
//
// This is NOT "response bytes == request bytes". It is: every required-present
// field of a replayable part survives parse -> re-serialize. The failure mode it
// catches is a struct field with `omitempty` on a VALUE type where the provider
// treats present-but-empty as DIFFERENT from absent — the class behind
// Anthropic #567 (empty "thinking") and #568 (empty tool_use "input").
//
// GEMINI-SPECIFIC SCHEMA NOTES (why the required-key set differs from Anthropic):
//
//   - functionCall.name — genuinely required. It has NO `omitempty` in the wire
//     type (geminiFunctionCall.Name `json:"name"`), so it is always present.
//     Asserted as required-present.
//
//   - functionCall.args — OPTIONAL per the Gemini API schema. Unlike Anthropic's
//     tool_use.input (which 400s "input: Field required" when absent), Gemini's
//     FunctionCall.args is a `google.protobuf.Struct` documented "Optional": a
//     no-argument call is valid on the wire as either `"args":{}` or with args
//     entirely absent. Verified against ai.google.dev's generate-content schema.
//     Therefore there is NO #568-analog for Gemini and — per the conservative
//     "only assert a key is required-present when the provider genuinely requires
//     it even when empty" rule — args is NOT asserted required. The no-arg cases
//     below assert only that `name` survives, and document that a dropped args is
//     schema-valid. (Tracker drops it: `Args map[string]any json:"args,omitempty"`
//     omits both a nil and an empty map.)
//
//   - thoughtSignature — Gemini 3's opaque reasoning-continuity blob, attached to
//     a functionCall part. It is `json:"thoughtSignature,omitempty"` and that
//     omitempty is CORRECT: absent means "no signature", which is a valid state,
//     so nothing is required-when-empty. The test pins BOTH directions — present
//     when the model emitted one (must round-trip), absent when it did not.
//
//   - Reasoning field: Gemini has no separate required-when-empty reasoning
//     content block on tracker's parse path (candidateContentParts only surfaces
//     text and functionCall). Reasoning continuity rides on thoughtSignature.
//     Documented here; nothing to assert.

// replayableParts serializes one assistant message through the ACTUAL
// request-translation entry point (translateRequest) and returns the decoded
// parts of the first content item as raw key maps, so presence/absence of each
// schema key is directly observable.
func replayableParts(t *testing.T, msg llm.Message) []map[string]json.RawMessage {
	t.Helper()
	body, err := translateRequest(&llm.Request{
		Model:    "gemini-x",
		Messages: []llm.Message{msg},
	})
	if err != nil {
		t.Fatalf("translateRequest failed: %v", err)
	}
	var wire struct {
		Contents []struct {
			Role  string                       `json:"role"`
			Parts []map[string]json.RawMessage `json:"parts"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("unmarshal request body: %v\nbody: %s", err, body)
	}
	if len(wire.Contents) != 1 {
		t.Fatalf("contents count = %d, want 1; body: %s", len(wire.Contents), body)
	}
	if len(wire.Contents[0].Parts) == 0 {
		t.Fatalf("content has no parts; body: %s", body)
	}
	return wire.Contents[0].Parts
}

// parseResponseMessage runs a Gemini response body through the ACTUAL
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

// streamMessage drives the ACTUAL Gemini SSE parser (parseSSE) over the given
// SSE body, feeds every emitted event through the real StreamAccumulator, and
// returns the accumulated assistant message. This exercises the stream path a
// replayable part travels when the call was streamed (the path that surfaced
// Anthropic #568) — for Gemini, processGeminiPart marshals functionCall.Args to
// JSON before the accumulator ever sees it.
func streamMessage(t *testing.T, sse string) llm.Message {
	t.Helper()
	a := New("")
	ch := make(chan llm.StreamEvent, 64)
	go func() {
		a.parseSSE(context.Background(), strings.NewReader(sse), ch, false, llm.NewStreamIdleGuard(0, func() {}))
		close(ch)
	}()
	acc := llm.NewStreamAccumulator()
	for ev := range ch {
		if ev.Type == llm.EventError {
			t.Fatalf("stream emitted error event: %v", ev.Err)
		}
		acc.Process(ev)
	}
	return acc.Response().Message
}

// sseChunk wraps a single Gemini candidate-parts payload as one SSE data line.
func sseChunk(parts string) string {
	return `data: {"candidates":[{"content":{"role":"model","parts":[` + parts +
		`]},"finishReason":"STOP"}]}` + "\n"
}

// candidateEnvelope wraps a single Gemini part in a non-streaming response body.
func candidateEnvelope(part string) string {
	return `{"candidates":[{"content":{"role":"model","parts":[` + part +
		`]},"finishReason":"STOP"}],"modelVersion":"gemini-x"}`
}

func TestConformanceReplayFidelity(t *testing.T) {
	cases := []struct {
		name string
		// build yields the assistant message to replay, exercising the real
		// parse path (response-parse or stream-accumulator).
		build func(t *testing.T) llm.Message
		// presentKeys must be present after re-serialization (empty value is fine
		// — presence is the point). Dotted paths navigate nested objects, e.g.
		// "functionCall.name".
		presentKeys []string
		// absentKeys must NOT be present after re-serialization.
		absentKeys []string
		// knownBug, if set, converts a missing required key into t.Skip(knownBug)
		// instead of a failure — for a case that fails on current main due to an
		// open bug. When the bug is fixed the case activates automatically. No
		// Gemini case sets this: every assertion here passes on current main.
		knownBug string
	}{
		{
			// Text part — required key is "text". Not a required-when-empty case
			// (Gemini does not require replaying an empty text part), so only the
			// normal non-empty shape is asserted.
			name: "text",
			build: func(t *testing.T) llm.Message {
				return parseResponseMessage(t, candidateEnvelope(
					`{"text":"hello"}`))
			},
			presentKeys: []string{"text"},
		},
		{
			// functionCall with arguments, no signature. name + args present;
			// thoughtSignature correctly ABSENT (pins the "omitted when absent"
			// direction the audit called correct).
			name: "function_call_with_args",
			build: func(t *testing.T) llm.Message {
				return parseResponseMessage(t, candidateEnvelope(
					`{"functionCall":{"name":"lookup","args":{"q":"x"}}}`))
			},
			presentKeys: []string{"functionCall", "functionCall.name", "functionCall.args"},
			absentKeys:  []string{"thoughtSignature"},
		},
		{
			// No-argument functionCall via the RESPONSE path. Gemini schema makes
			// args OPTIONAL, so tracker's omitempty-dropped args is valid — we
			// assert only the genuinely-required name. (Contrast Anthropic's
			// tool_use.input, which IS required-present.)
			name: "function_call_no_args_response_path",
			build: func(t *testing.T) llm.Message {
				return parseResponseMessage(t, candidateEnvelope(
					`{"functionCall":{"name":"now"}}`))
			},
			presentKeys: []string{"functionCall", "functionCall.name"},
		},
		{
			// The SAME no-argument functionCall reaching the request via the STREAM
			// accumulator (the #568 path for Anthropic). Gemini's processGeminiPart
			// marshals a nil args map to "null" before the accumulator sees it, so
			// Arguments is never the empty string that broke Anthropic. args is
			// optional for Gemini regardless, so we assert only that name survives.
			name: "function_call_no_args_stream_path",
			build: func(t *testing.T) llm.Message {
				return streamMessage(t, sseChunk(
					`{"functionCall":{"name":"now"}}`))
			},
			presentKeys: []string{"functionCall", "functionCall.name"},
		},
		{
			// functionCall carrying a thoughtSignature via the RESPONSE path. The
			// signature MUST round-trip (Gemini 3 rejects a replayed reasoning turn
			// whose signature was dropped). Pins the "present when present"
			// direction.
			name: "function_call_thought_signature_response_path",
			build: func(t *testing.T) llm.Message {
				return parseResponseMessage(t, candidateEnvelope(
					`{"functionCall":{"name":"lookup","args":{"q":"x"}},"thoughtSignature":"sig-abc"}`))
			},
			presentKeys: []string{"functionCall", "functionCall.name", "thoughtSignature"},
		},
		{
			// functionCall + thoughtSignature via the STREAM path — the signature
			// must survive accumulate -> re-serialize as well.
			name: "function_call_thought_signature_stream_path",
			build: func(t *testing.T) llm.Message {
				return streamMessage(t, sseChunk(
					`{"functionCall":{"name":"lookup","args":{"q":"x"}},"thoughtSignature":"sig-abc"}`))
			},
			presentKeys: []string{"functionCall", "functionCall.name", "thoughtSignature"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parts := replayableParts(t, tc.build(t))
			part := parts[0]

			for _, key := range tc.presentKeys {
				if !hasPath(t, part, key) {
					if tc.knownBug != "" {
						t.Skip(tc.knownBug)
					}
					t.Errorf("required-present key %q is MISSING from replayed part; keys present: %v",
						key, keysOf(part))
				}
			}

			for _, key := range tc.absentKeys {
				if hasPath(t, part, key) {
					t.Errorf("key %q must be ABSENT from replayed part but is present", key)
				}
			}
		})
	}
}

// hasPath reports whether a dotted key path resolves to a present key in the
// decoded part (e.g. "functionCall.name" descends into the functionCall object).
func hasPath(t *testing.T, part map[string]json.RawMessage, path string) bool {
	t.Helper()
	segs := strings.Split(path, ".")
	cur := part
	for i, seg := range segs {
		raw, ok := cur[seg]
		if !ok {
			return false
		}
		if i == len(segs)-1 {
			return true
		}
		var next map[string]json.RawMessage
		if err := json.Unmarshal(raw, &next); err != nil {
			return false
		}
		cur = next
	}
	return true
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
