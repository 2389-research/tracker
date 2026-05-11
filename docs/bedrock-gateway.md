# Using Tracker with the Bedrock Gateway

The [2389 Bedrock Gateway](https://github.com/2389-research/gateway) is a
Cloudflare Worker that accepts native Anthropic, OpenAI, and Gemini SDK
requests and routes them to AWS Bedrock via Cloudflare AI Gateway. Live URL:

```text
https://bedrock-gateway.2389-research-inc.workers.dev
```

You can run tracker pipelines against it with no code changes — only env
vars or the `--gateway-url` flag.

## TL;DR

The simplest path is `--gateway-url` plus a CF AI Gateway token in the
per-provider auth env var:

```bash
export ANTHROPIC_API_KEY="<CF_AIG_TOKEN>"   # not your normal Anthropic key

tracker --gateway-url https://bedrock-gateway.2389-research-inc.workers.dev \
  my_pipeline.dip
```

Tracker appends `/anthropic`, `/openai`, `/google-ai-studio`, or `/compat`
to the gateway URL automatically per provider. These match Cloudflare AI
Gateway's native routing format, which the bedrock gateway accepts as of
gateway [#5](https://github.com/2389-research/gateway/issues/5).

For mixed-provider pipelines, set the corresponding env var
(`GEMINI_API_KEY`, `OPENAI_COMPAT_API_KEY`) to the CF AIG token — same
value, different headers per SDK.

## How `--gateway-url` resolves to a path

The bedrock gateway exposes endpoints in two shapes:

```text
<gateway>/anthropic/v1/messages              ← CF AIG native prefix shape
<gateway>/v1/messages                        ← flat shape
```

`--gateway-url` (and `TRACKER_GATEWAY_URL`) targets the **prefix shape**:
tracker's `ResolveProviderBaseURL` appends a per-provider suffix
(`/anthropic`, `/openai`, `/google-ai-studio`, `/compat`) before the SDK
appends its own path.

The per-provider `*_BASE_URL` env vars (`ANTHROPIC_BASE_URL`, etc.)
target the **flat shape**: tracker hands the URL to the SDK as-is, no
suffix added. They always win over `--gateway-url`, so set them when you
want to point only one provider at the gateway and leave others on their
native APIs.

## Provider compatibility matrix

| Tracker provider | Gateway endpoint tracker hits | Works? |
|------------------|-------------------------------|--------|
| `anthropic` | `<base>/anthropic/v1/messages` (or `<base>/v1/messages` via `ANTHROPIC_BASE_URL`) | ✓ |
| `openai-compat` | `<base>/compat/v1/chat/completions` (or `<base>/v1/chat/completions`) | ✓ |
| `gemini` | `<base>/google-ai-studio/v1beta/models/<m>:generateContent` (or `<base>/v1beta/...`) | ✓ |
| `openai` | `<base>/v1/responses` (OpenAI **Responses API**) | ✗ — gateway [#3](https://github.com/2389-research/gateway/issues/3) |

**OpenAI:** tracker's `openai` adapter speaks the Responses API. The
gateway implements only Chat Completions. To route OpenAI-style requests
through the gateway, use `provider: openai-compat` until gateway #3 lands.

## Authentication

The gateway accepts a [Cloudflare AI Gateway](https://developers.cloudflare.com/ai-gateway/)
token in whichever auth header your SDK normally uses. Tracker reads keys
from the conventional provider env vars, so set them to your CF AIG token:

| Provider | Env var |
|----------|---------|
| `anthropic` | `ANTHROPIC_API_KEY` |
| `gemini` | `GEMINI_API_KEY` |
| `openai-compat` | `OPENAI_COMPAT_API_KEY` |

CF AI Gateway tokens for the 2389 worker are provisioned through the
2389 Cloudflare account; ask in `#ai-platform` (or whoever currently
owns the gateway) for one if you don't already have access. The token
is the same value regardless of which SDK header it goes in.

A normal Anthropic / OpenAI / Google key will be rejected by the gateway
— it forwards the value as `cf-aig-authorization`, not as a provider key.

## Configuration recipes

### Recommended: `--gateway-url` (one URL for all providers)

```bash
export ANTHROPIC_API_KEY="<CF_AIG_TOKEN>"
export GEMINI_API_KEY="<CF_AIG_TOKEN>"        # only if you have gemini agents
export OPENAI_COMPAT_API_KEY="<CF_AIG_TOKEN>" # only if you have openai-compat agents

tracker --gateway-url https://bedrock-gateway.2389-research-inc.workers.dev \
  my_pipeline.dip
```

In your `.dip` files:

```dip
agent Plan {
  provider: anthropic
  model: claude-sonnet-4-6
  prompt: "..."
}

agent Search {
  provider: gemini
  model: gemini-2.5-pro
  prompt: "..."
}
```

The same Claude aliases the gateway supports apply: `claude-opus-4-6`,
`claude-sonnet-4-6`, `claude-haiku-4-5`, `claude-3.7-sonnet`, etc. (see
the [gateway README](https://github.com/2389-research/gateway#supported-models)
for the full list). You can also pass a raw Bedrock inference-profile ID
(e.g. `us.anthropic.claude-sonnet-4-6`) and it will pass through.

`TRACKER_GATEWAY_URL` works the same as the flag if you'd rather set it
once in your shell.

### Per-provider `*_BASE_URL` (override `--gateway-url`)

To point only one provider at the gateway and leave others on their
native APIs, use the per-provider env vars instead — these target the
gateway's flat-shape routes and always win over `--gateway-url`:

```bash
export ANTHROPIC_API_KEY="<CF_AIG_TOKEN>"
export ANTHROPIC_BASE_URL="https://bedrock-gateway.2389-research-inc.workers.dev"

# Gemini stays on the real Google API
export GEMINI_API_KEY="<real-google-key>"
```

For OpenAI-compat, a trailing `/v1` is fine — tracker's `openai-compat`
adapter strips it and re-appends `/v1/chat/completions`:

```bash
export OPENAI_COMPAT_API_KEY="<CF_AIG_TOKEN>"
export OPENAI_COMPAT_BASE_URL="https://bedrock-gateway.2389-research-inc.workers.dev/v1"
```

## Verifying it's working

Tracker doesn't surface the resolved provider URL in `tracker doctor`
or in the activity log — both report only the provider name and key
status. There's no in-tool way to confirm the gateway is being hit
short of inspecting the Cloudflare AI Gateway dashboard's request log
(or running with `TRACKER_DEBUG=1`, which prints diagnostic detail
on empty/failed responses but not on the happy path).

A short, reliable check sequence:

1. **Confirm the env vars are set** in the shell tracker will run in:

   ```bash
   echo "$ANTHROPIC_API_KEY" | head -c 12; echo
   ```

   `tracker doctor` will tell you whether the API key passes
   validation against the gateway (because doctor's auth probe goes
   through the configured base URL), but it does not echo the URL
   back at you.

2. **Run a one-shot agent node** against a trivial pipeline and check
   the [Cloudflare AI Gateway dashboard](https://dash.cloudflare.com/?to=/:account/ai/ai-gateway)
   for a request matching the timing — that's the authoritative
   confirmation that traffic actually flowed through the gateway.

3. If a call fails, `tracker diagnose` surfaces the error body the
   provider returned, which carries gateway-specific phrasing (e.g.
   "Bedrock model not found") that distinguishes a gateway-routed
   failure from a direct-API one.

## Gateway limitations to know about

These come from the [gateway README](https://github.com/2389-research/gateway#known-limitations)
and apply regardless of which SDK you use:

- **OpenAI Responses API not yet implemented** (gateway
  [#3](https://github.com/2389-research/gateway/issues/3)). Use
  `provider: openai-compat` until it lands.
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
`openai-compat`, `gemini`), not "bedrock," because that's the API
surface in use. The dollar cost is computed from tracker's per-provider
price table, so it reflects published Anthropic/OpenAI list prices, not
your actual AWS Bedrock invoice.

## Troubleshooting

**401 / auth failures.** You're using a real Anthropic/OpenAI/Google key.
The gateway needs a Cloudflare AI Gateway token.

**`provider: openai` nodes fail with 404 on `/v1/responses`.** Tracker's
OpenAI adapter targets the Responses API; the gateway implements Chat
Completions. Switch the node to `provider: openai-compat` until gateway
[#3](https://github.com/2389-research/gateway/issues/3) lands.

**Timeouts at ~30 s.** Bedrock's per-request limit. Reduce prompt size
or break the work into more turns.

## See also

- [Gateway repo](https://github.com/2389-research/gateway) — full alias
  list, request/response shapes, deployment.
- [`docs/architecture/llm.md`](architecture/llm.md) — tracker's provider
  adapter layer.
- `tracker.ResolveProviderBaseURL` in `tracker.go` — exact precedence
  rules for base-URL resolution.
