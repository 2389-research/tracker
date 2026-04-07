# OpenAI-Compatible Chat Completions Provider (`openai-compat`)

**Date**: 2026-04-06
**Status**: Design
**Author**: Claude + Doctor Biz

## Problem

Tracker's OpenAI provider uses the Responses API (`/v1/responses`), which only OpenAI's servers support. OpenAI-compatible servers (LM Studio, Ollama, vLLM, OpenRouter, Together, Groq) all implement the Chat Completions API (`/v1/chat/completions`). There is no way to use these servers with tracker today.

## Solution

Add a new provider `openai-compat` that speaks the OpenAI Chat Completions API. This is a standalone package following the existing provider pattern — not a mode flag on the existing OpenAI adapter.

## Provider Identity

- **Name**: `openai-compat`
- **In .dip files**: `provider: openai-compat`
- **Default base URL**: `https://openrouter.ai/api` (overridable)
- **Env vars**:
  - `OPENAI_COMPAT_API_KEY` (primary) → `OPENAI_API_KEY` (fallback)
  - `OPENAI_COMPAT_BASE_URL` (override)

## Files

### New files

| File | Purpose |
|------|---------|
| `llm/openaicompat/adapter.go` | HTTP client, Complete(), Stream(), SSE parsing |
| `llm/openaicompat/translate.go` | Request/response translation to Chat Completions wire format |
| `llm/openaicompat/adapter_test.go` | End-to-end SSE stream tests |
| `llm/openaicompat/translate_test.go` | Request/response serialization tests |

### Modified files

| File | Change |
|------|--------|
| `llm/client.go` | Add `"openai-compat"` to `providerEnvKeys` with fallback chain |
| `tracker.go` | Add constructor in `buildClient()` |
| `cmd/tracker/run.go` | Add constructor in `buildLLMClient()` |

## Wire Format: Chat Completions API

### Request (`POST /v1/chat/completions`)

```json
{
  "model": "qwen/qwen3-coder-next",
  "messages": [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": "Hello"},
    {"role": "assistant", "content": "Hi!", "tool_calls": [
      {"id": "call_1", "type": "function", "function": {"name": "read_file", "arguments": "{\"path\": \"main.go\"}"}}
    ]},
    {"role": "tool", "tool_call_id": "call_1", "content": "package main..."}
  ],
  "tools": [
    {"type": "function", "function": {"name": "read_file", "description": "...", "parameters": {...}}}
  ],
  "tool_choice": "auto",
  "max_tokens": 16384,
  "temperature": 0.7,
  "response_format": {"type": "json_object"},
  "stream": true
}
```

### Key differences from Responses API

| Aspect | Responses API (existing `openai`) | Chat Completions (`openai-compat`) |
|--------|----------------------------------|-----------------------------------|
| Endpoint | `/v1/responses` | `/v1/chat/completions` |
| System message | Extracted to top-level `instructions` | In `messages[]` as `{role: "system"}` |
| Message format | Flat `input[]` with role/type fields | `messages[]` with role/content objects |
| Tool calls | Separate `{type: "function_call"}` items in input | Nested in assistant message `tool_calls[]` |
| Tool results | `{type: "function_call_output", call_id, output}` | `{role: "tool", tool_call_id, content}` |
| Tool defs | `{type: "function", name, parameters}` | `{type: "function", function: {name, parameters}}` |
| Response format | `text: {format: {type, schema}}` | `response_format: {type}` at top level |
| Response body | `{output: [{type, content}]}` | `{choices: [{message: {content, tool_calls}}]}` |
| SSE events | Typed events (`response.output_text.delta`, etc.) | Simple `data: {choices: [{delta: {...}}]}` |
| Finish reason | `status` field on response object | `choices[0].finish_reason` |

## Translation Functions

### Request translation

- `translateRequest(req *llm.Request) ([]byte, error)` — builds the Chat Completions JSON body
- `translateMessages(messages []llm.Message) []chatMessage` — converts all messages including system (no extraction)
- `translateAssistantMessage(content []llm.ContentPart) chatMessage` — nests tool calls in the message
- `translateToolResultMessage(content []llm.ContentPart) []chatMessage` — produces `role: "tool"` messages
- `translateToolDefs(tools []llm.ToolDefinition) []chatTool` — wraps in `{type: "function", function: {...}}`
- `translateResponseFormat(rf *llm.ResponseFormat) *chatResponseFormat` — top-level format

### Response translation

- `translateResponse(raw []byte) (*llm.Response, error)` — parses `choices[0].message`
- `translateFinishReason(reason string) llm.FinishReason` — maps `stop`/`tool_calls`/`length`/`content_filter`

### SSE handling

Chat Completions SSE is simpler than Responses API. Each event is:
```
data: {"id":"...","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}
```

Tool call deltas stream as:
```
data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":""}}]}}]}
data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"pa"}}]}}]}
```

End signal:
```
data: [DONE]
```

The SSE parser accumulates tool call arguments across deltas (keyed by index), then emits complete tool calls at the end.

## Adapter Structure

```go
type Adapter struct {
    apiKey     string
    baseURL    string
    httpClient *http.Client
}

type Option func(*Adapter)

func WithBaseURL(url string) Option { ... }
func WithHTTPClient(client *http.Client) Option { ... }

func New(apiKey string, opts ...Option) *Adapter { ... }
func (a *Adapter) Name() string { return "openai-compat" }
func (a *Adapter) Complete(ctx, req) (*llm.Response, error) { ... }
func (a *Adapter) Stream(ctx, req) <-chan llm.StreamEvent { ... }
func (a *Adapter) Close() error { return nil }
```

Follows the exact same pattern as `llm/openai/adapter.go`. Base URL normalization strips trailing `/v1` (same logic).

## Registration

### `llm/client.go`

```go
var providerEnvKeys = map[string][]string{
    "anthropic":    {"ANTHROPIC_API_KEY"},
    "openai":       {"OPENAI_API_KEY"},
    "gemini":       {"GEMINI_API_KEY", "GOOGLE_API_KEY"},
    "openai-compat": {"OPENAI_COMPAT_API_KEY", "OPENAI_API_KEY"},
}
```

### `tracker.go` and `cmd/tracker/run.go`

```go
"openai-compat": func(key string) (llm.ProviderAdapter, error) {
    var opts []openaicompat.Option
    if base := os.Getenv("OPENAI_COMPAT_BASE_URL"); base != "" {
        opts = append(opts, openaicompat.WithBaseURL(base))
    }
    return openaicompat.New(key, opts...), nil
},
```

## Headers

Standard OpenAI-compatible headers:
```
Content-Type: application/json
Authorization: Bearer {api_key}
```

OpenRouter also supports optional `HTTP-Referer` and `X-Title` headers. These are nice-to-have but not required. We can add them later via provider options if needed.

## Error Handling

Reuse the same `llm.ErrorFromStatusCode()` function. Chat Completions errors have the same structure:
```json
{"error": {"message": "...", "type": "...", "code": "..."}}
```

## What This Does NOT Include

- Model catalog entries for openai-compat (models are dynamic / server-dependent)
- OpenRouter-specific headers (HTTP-Referer, X-Title) — add later if needed
- Reasoning effort / extended thinking (not supported by Chat Completions)
- Response format `json_schema` mode — `translateResponseFormat` passes through `json_object` but ignores `json_schema` (most compat servers don't support structured output). If a .dip node requests `response_format: json_object`, it works. If it requests `json_schema`, the adapter silently drops it (no error).

## Verification

1. `go build ./...` — compiles
2. `go test ./llm/openaicompat/...` — unit tests pass
3. `go test ./... -short` — all 14+ packages pass
4. Manual test with LM Studio:
   ```
   OPENAI_COMPAT_BASE_URL=http://localhost:1234/v1 OPENAI_COMPAT_API_KEY=lm-studio tracker build-codeagent-local.dip
   ```
5. Update `build-codeagent-local.dip` to use `provider: openai-compat`
