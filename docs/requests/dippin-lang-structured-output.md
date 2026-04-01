# Feature Request: Structured Output Support in Dippin-Lang IR

**From:** Tracker team
**To:** Dippin-lang team
**Date:** 2026-03-31
**Priority:** High — blocking production use of interview mode
**Status:** RESOLVED — dippin-lang v0.16.0 shipped all requested changes. Tracker integration complete.

---

## Summary

Tracker needs to force LLM APIs to produce structured JSON output on specific agent nodes. The LLM layer already supports `ResponseFormat` across all three providers (Anthropic, OpenAI, Gemini). The agent session and codergen handler are now wired to read `response_format` and `response_schema` from node attributes. **The missing piece is dippin-lang IR support** — there is no way to express `response_format` in `.dip` files today.

## What We Need

### 1. `response_format` field on `AgentConfig`

```go
type AgentConfig struct {
    // ... existing fields ...
    ResponseFormat string // "json_object" or "json_schema"
    ResponseSchema string // JSON schema string (when ResponseFormat is "json_schema")
}
```

### 2. `.dip` syntax

```dip
agent GenerateQuestions
  label: "Generate Interview Questions"
  model: claude-opus-4-6
  response_format: json_object
  prompt:
    Output a JSON object with interview questions...

agent StrictQuestions
  label: "Generate Typed Questions"
  model: gpt-5.4
  response_format: json_schema
  response_schema: |
    {
      "type": "object",
      "properties": {
        "questions": {
          "type": "array",
          "items": {
            "type": "object",
            "properties": {
              "text": {"type": "string"},
              "context": {"type": "string"},
              "options": {"type": "array", "items": {"type": "string"}}
            },
            "required": ["text"]
          }
        }
      },
      "required": ["questions"]
    }
  prompt:
    Generate interview questions for the developer...
```

### 3. Validation rules

- `response_format` must be one of: `json_object`, `json_schema`, or empty
- `response_schema` requires `response_format: json_schema`
- `response_schema` must be valid JSON (validate at parse time)
- Lint warning if `response_format: json_schema` but no `response_schema` provided

## Why This Matters

### The Problem

Interview mode generates structured questions via LLM agents. The handler parses the agent's `last_response` for JSON questions. But LLMs are unreliable at producing pure JSON when they also have tool access:

1. Agent calls tools (reads files) → `last_response` captures "I've written the results" not the JSON
2. Agent outputs JSON wrapped in markdown fences or with prose preamble → fragile extraction
3. Agent uses all turns on tool calls → `last_response` is empty

### Current Workaround (Fragile)

We use tool nodes to `cat` file contents into `tool_stdout`, then `max_turns: 1` agents that receive everything via `${ctx.tool_stdout}`. This is brittle:
- `max_turns: 1` still allows one tool call that consumes the turn with no text output
- The LLM may ignore "DO NOT use tools" instructions
- Extra tool nodes add pipeline complexity for what should be a model configuration

### What `response_format` Solves

All three providers support forcing JSON output at the API level:

| Provider | `json_object` | `json_schema` |
|----------|--------------|---------------|
| **Anthropic** | System instruction: "Respond with valid JSON" | System instruction + schema |
| **OpenAI** | Native `response_format: {type: "json_object"}` | Native strict JSON schema mode |
| **Gemini** | `responseMimeType: application/json` | `responseMimeType` + `responseSchema` |

With `response_format: json_object`, the model is **forced** to output valid JSON. No tool calls, no prose, no fences. The tracker LLM layer already handles all the provider-specific translation — we just need the `.dip` syntax to thread the attribute through.

## Tracker-Side Implementation (Already Done)

The following plumbing is already merged on `feat/interview-mode`:

1. **`agent/config.go`**: `SessionConfig.ResponseFormat` and `SessionConfig.ResponseSchema` fields
2. **`agent/session_run.go`**: `buildResponseFormat()` method, wired into `doLLMCall()`
3. **`pipeline/handlers/codergen.go`**: `applyResponseFormat()` reads from `node.Attrs["response_format"]` and `node.Attrs["response_schema"]`
4. **`pipeline/dippin_adapter.go`**: Ready to extract from `AgentConfig` once the fields exist

The moment dippin-lang adds `ResponseFormat` and `ResponseSchema` to `AgentConfig`, tracker will automatically thread them through to the LLM API. Zero additional tracker changes needed.

## Secondary Request: Generic `Params` Map on `AgentConfig`

For future-proofing, consider adding a generic `Params map[string]string` to `AgentConfig` (like `SubgraphConfig` already has). This would let tracker consume new provider-specific features without requiring dippin-lang IR changes each time:

```go
type AgentConfig struct {
    // ... existing typed fields ...
    Params map[string]string // Generic key-value pairs passed through to runtime
}
```

```dip
agent MyNode
  params:
    response_format: json_object
    backend: claude-code
    permission_mode: auto
```

This addresses the broader pattern where tracker's runtime capabilities outpace the dippin-lang IR schema. The `Params` map is a pressure valve that lets tracker innovate without blocking on IR changes.

## Testing

When implemented, the following dippin validation rules should apply:

```
DIP_NEW_001: response_format must be "json_object" or "json_schema"
DIP_NEW_002: response_schema requires response_format: json_schema
DIP_NEW_003: response_schema must be valid JSON
DIP_LINT_NEW: warning if json_schema without response_schema
```

Tracker will add integration tests for the full path:
- `.dip` → IR → adapter → node attrs → codergen → agent session → LLM request
- All three providers
- json_object and json_schema modes
