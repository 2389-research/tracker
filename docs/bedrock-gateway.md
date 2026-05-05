# Using Tracker with the Bedrock Gateway

The [2389 Bedrock Gateway](https://github.com/2389-research/gateway) is a
Cloudflare Worker that accepts native Anthropic, OpenAI, and Gemini SDK
requests and routes them to AWS Bedrock via Cloudflare AI Gateway. Live URL:

```
https://bedrock-gateway.2389-research-inc.workers.dev
```

You can run tracker pipelines against it with no code changes — only env
vars. This doc walks through the working configurations and the two known
incompatibilities.

## TL;DR

The cleanest path is to use tracker's `anthropic` provider with Claude
models. Set two env vars and run as normal:

```bash
export ANTHROPIC_API_KEY="<CF_AIG_TOKEN>"   # not your normal Anthropic key
export ANTHROPIC_BASE_URL="https://bedrock-gateway.2389-research-inc.workers.dev"

tracker run my_pipeline.dip
```

Any agent node with `provider: anthropic` and a Claude model alias
(`claude-sonnet-4-6`, `claude-haiku-4-5`, …) will route through Bedrock.

## Why not `--gateway-url`?

Tracker has a `--gateway-url` flag (and `TRACKER_GATEWAY_URL` env var)
designed for Cloudflare AI Gateway's *native* routing format, where the
gateway URL has provider segments appended:

```
<gateway>/anthropic     → forwards to api.anthropic.com
<gateway>/openai        → forwards to api.openai.com
<gateway>/google-ai-studion → forwards to Gemini
<gateway>/compat        → OpenAI-compatible passthrough
```

The Bedrock Gateway worker does **not** use that layout — it exposes the
SDKs' literal paths at the root (`/v1/messages`, `/v1/chat/completions`,
…). So `--gateway-url https://bedrock-gateway.2389-research-inc.workers.dev`
will produce 404s.

Use the per-provider `*_BASE_URL` env vars instead (next section).

## Provider compatibility matrix

The gateway implements a specific set of endpoints (one per SDK's most
common path). Tracker's adapters happen to pick different endpoints in
two cases — both are choices on tracker's side, not gaps in the gateway:

| Tracker provider | Path tracker calls | On the gateway | Works? |
|------------------|---------------------|----------------|--------|
| `anthropic` | `<base>/v1/messages` | implemented | yes |
| `openai-compat` | `<base>/v1/chat/completions` | implemented | yes |
| `openai` | `<base>/v1/responses` (OpenAI **Responses API**) | not implemented | no |
| `gemini` | `<base>/v1beta/models/<m>:generateContent` | gateway uses `/v1` | no |

**OpenAI:** tracker's `openai` adapter speaks the Responses API. The
gateway implements only Chat Completions (which is what the OpenAI
Python SDK's `client.chat.completions.create(...)` calls). To route
OpenAI-style requests through the gateway, use `provider: openai-compat`.

**Gemini:** tracker calls Google's `v1beta` REST path; the gateway
exposes `v1`. Both are valid Google-defined paths, just different API
versions. There's no plumbing today to point tracker's Gemini adapter
at `v1`. Until that lands, route Gemini-flavored work through
`anthropic` — the gateway maps `gemini-2.5-pro` → Claude Sonnet 4.6
anyway.

## Authentication

The gateway accepts a [Cloudflare AI Gateway](https://developers.cloudflare.com/ai-gateway/)
token in whichever auth header your SDK normally uses. Tracker reads keys
from the conventional provider env vars, so set them to your CF AIG token:

| Provider | Env var |
|----------|---------|
| `anthropic` | `ANTHROPIC_API_KEY` |
| `openai-compat` | `OPENAI_COMPAT_API_KEY` |

Need a token? Ask Dylan.

A normal Anthropic / OpenAI key will be rejected by the gateway — it
forwards the value as `cf-aig-authorization`, not as a provider key.

## Configuration recipes

### Anthropic-style (recommended)

```bash
export ANTHROPIC_API_KEY="<CF_AIG_TOKEN>"
export ANTHROPIC_BASE_URL="https://bedrock-gateway.2389-research-inc.workers.dev"
```

In your `.dip` files:

```dip
agent Plan {
  provider: anthropic
  model: claude-sonnet-4-6
  prompt: "..."
}
```

The same Claude aliases the gateway supports apply: `claude-opus-4-6`,
`claude-sonnet-4-6`, `claude-haiku-4-5`, `claude-3.7-sonnet`, etc. (see
the [gateway README](https://github.com/2389-research/gateway#supported-models)
for the full list). You can also pass a raw Bedrock inference-profile ID
(e.g. `us.anthropic.claude-sonnet-4-6`) and it will pass through.

### OpenAI-style (chat-completions)

Use the `openai-compat` provider, not `openai`. Tracker's `openai`
provider speaks the Responses API, which the gateway doesn't implement.

```bash
export OPENAI_COMPAT_API_KEY="<CF_AIG_TOKEN>"
export OPENAI_COMPAT_BASE_URL="https://bedrock-gateway.2389-research-inc.workers.dev/v1"
```

Note the trailing `/v1` — tracker's `openai-compat` adapter strips a
trailing `/v1` and re-appends `/v1/chat/completions`, so either form
works.

```dip
agent Plan {
  provider: openai-compat
  model: gpt-4o          # gateway alias → Claude Sonnet 4.6
  prompt: "..."
}
```

### Mixing native APIs and the gateway

Per-provider `*_BASE_URL` env vars only affect the providers you set, so
you can run some nodes through the gateway and others against their
native APIs in the same pipeline. For example: route `provider: anthropic`
nodes through Bedrock while keeping `provider: gemini` on the real
Gemini API by setting `ANTHROPIC_BASE_URL` but not `GEMINI_BASE_URL`.

## Verifying it's working

```bash
tracker doctor
```

This prints, per provider, whether a key is set and which base URL is in
effect. With the gateway configured you should see something like:

```
anthropic: API key set, base URL: https://bedrock-gateway.2389-research-inc.workers.dev
```

For a quick end-to-end check, run any built-in workflow with a one-shot
agent node — the activity log will show the request hitting the gateway,
and `tracker diagnose` exposes any non-2xx responses.

## Gateway limitations to know about

These come from the [gateway README](https://github.com/2389-research/gateway#known-limitations)
and apply regardless of which SDK you use:

- **30-second Bedrock timeout** per request. Long-running agent turns
  with large prompts may hit it; split work across smaller turns or
  reduce context.
- **1 MB request-body limit.** Very large transcripts or attachments
  will be rejected before reaching Bedrock.
- **Base64-only images.** HTTP image URLs are silently treated as text,
  not fetched.
- **No `n > 1`.** Multi-completion isn't supported. Tracker doesn't use
  `n > 1` itself, but custom integrations might.
- **No Bedrock-specific features** (guardrails, etc.) — anything that
  isn't in the OpenAI/Anthropic/Gemini SDK shape isn't exposed.

## Cost accounting

Tracker's `UsageSummary` and `--max-cost` budget guard work normally —
the gateway returns standard token counts in each SDK's native response
shape, and tracker's `llm.TokenTracker` records them per provider as
usual. Note that the *provider* tracker sees is `anthropic` (or
`openai-compat`), not "bedrock," because that's the API surface in use.
The dollar cost is computed from tracker's per-provider price table, so
it reflects published Anthropic/OpenAI list prices, not your actual AWS
Bedrock invoice.

## Troubleshooting

**404s on every request.** You probably set `--gateway-url` /
`TRACKER_GATEWAY_URL`. Unset it and use `ANTHROPIC_BASE_URL` /
`OPENAI_COMPAT_BASE_URL` instead — see "Why not `--gateway-url`?" above.

**401 / auth failures.** You're using a real Anthropic/OpenAI key. The
gateway needs a Cloudflare AI Gateway token.

**`provider: openai` nodes fail with 404 on `/v1/responses`.** Tracker's
OpenAI adapter targets the Responses API; the gateway implements Chat
Completions. Switch the node to `provider: openai-compat`.

**`provider: gemini` nodes fail with 404 on `/v1beta/...`.** Tracker
hits Google's `v1beta` path; the gateway is on `v1`. Route the node
through `provider: anthropic` with a Claude alias instead.

**Timeouts at ~30 s.** Bedrock's per-request limit. Reduce prompt size
or break the work into more turns.

## See also

- [Gateway repo](https://github.com/2389-research/gateway) — full alias
  list, request/response shapes, deployment.
- [`docs/architecture/llm.md`](architecture/llm.md) — tracker's provider
  adapter layer.
- `tracker.ResolveProviderBaseURL` in `tracker.go` — exact precedence
  rules for base-URL resolution.
