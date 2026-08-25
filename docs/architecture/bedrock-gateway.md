# Gateway routing setup (`TRACKER_GATEWAY_URL` / `TRACKER_GATEWAY_KIND`)

Operator recipe for pointing tracker's provider SDKs at a gateway instead of
the public provider endpoints. Two gateway kinds are supported: **Cloudflare AI
Gateway** (`cf-aig`, the default) and the **2389 bedrock-gateway Worker**
(`bedrock`). The kind selects the per-provider URL path suffix that tracker
appends to `TRACKER_GATEWAY_URL`.

The routing logic and fail-closed contract live in
[`../../tracker.go`](../../tracker.go) (`gatewaySuffix`,
`resolveProviderBaseURLWithGateway`, `ErrGatewayRouteRefused`); the base-URL
resolution order is documented in [`./llm.md`](./llm.md#base-url-resolution).
This page is the operator-facing setup guide.

## Precedence

Base URL resolution consults three sources, in order:

1. **Per-provider `<PROVIDER>_BASE_URL`** (`ANTHROPIC_BASE_URL`,
   `OPENAI_BASE_URL`, `GEMINI_BASE_URL`, `OPENAI_COMPAT_BASE_URL`) — wins
   unconditionally. Use it to surgically override one provider while leaving the
   rest on the gateway.
2. **The gateway URL + kind-dependent suffix** — the gateway fallback. The URL
   comes from `Config.GatewayURL` (library, passed programmatically) first, then
   the `TRACKER_GATEWAY_URL` env var; likewise `Config.GatewayKind` then
   `TRACKER_GATEWAY_KIND`. The `--gateway-url` / `--gateway-kind` CLI flags set
   those env vars.
3. **Empty** — the provider SDK's own default endpoint.

So a per-provider base URL always beats gateway routing for that provider.

## Cloudflare AI Gateway (`cf-aig`, default)

The default kind. `TRACKER_GATEWAY_KIND` unset (or `cf-aig`) uses Cloudflare AI
Gateway path conventions. Existing setups need no changes.

```sh
export TRACKER_GATEWAY_URL=https://gateway.ai.cloudflare.com/v1/<account>/<gateway>
# TRACKER_GATEWAY_KIND unset → cf-aig
export ANTHROPIC_API_KEY=<key>
export OPENAI_API_KEY=<key>
export GEMINI_API_KEY=<key>
```

Per-provider suffixes appended to the gateway URL:

| Provider | Suffix |
|----------|--------|
| `anthropic` | `/anthropic` |
| `openai` | `/openai` |
| `gemini` | `/google-ai-studio` |
| `openai-compat` | `/compat` |

## Bedrock gateway (`bedrock`)

The 2389 bedrock-gateway Worker accepts native Anthropic/OpenAI/Gemini SDK
requests and translates them to AWS Bedrock Converse. One gateway URL plus one
Cloudflare AI Gateway token (reused as all three `*_API_KEY` vars) routes every
provider through Bedrock:

```sh
export TRACKER_GATEWAY_URL=https://bedrock-gateway.<your-worker>.workers.dev
export TRACKER_GATEWAY_KIND=bedrock          # or: --gateway-kind bedrock
# One CF AIG token, reused for all three providers:
export ANTHROPIC_API_KEY=<cf-aig-token>
export OPENAI_API_KEY=<cf-aig-token>
export GEMINI_API_KEY=<cf-aig-token>
```

The CLI equivalents are `--gateway-url` and `--gateway-kind`; the library
equivalents are `Config.GatewayURL` and `Config.GatewayKind`.

Per-provider suffixes under `bedrock` (from `gatewaySuffix` in
[`../../tracker.go`](../../tracker.go)) and the request path tracker's native
adapters actually issue against the gateway — useful when debugging the Worker:

| Provider | Suffix | Resulting request path on the gateway |
|----------|--------|----------------------------------------|
| `anthropic` | `""` (none) | adapter appends `/v1/messages` → `<gateway>/v1/messages` |
| `openai` | `/v1` | adapter uses the Responses API: it strips a trailing `/v1` from the base, then appends `/v1/responses` → `<gateway>/v1/responses` |
| `gemini` | `/v1` | adapter appends `/v1beta/models/{model}:generateContent` → `<gateway>/v1/v1beta/models/{model}:generateContent` |
| `openai-compat` | — | **refuses to route** (see caveats) |

The OpenAI trailing-`/v1` normalization is documented in
[`./llm.md`](./llm.md#base-url-resolution); it means the `/v1` suffix and the
adapter's own `/v1/responses` path do not double up.

## Caveats (bedrock kind only)

These are properties of the bedrock gateway as it stands today. None require a
tracker code change to resolve — tracker is a pure passthrough and inherits
gateway-side improvements transparently (see
[`../../CLAUDE.md`](../../CLAUDE.md), Architecture Gotchas → "Gateway upstream
transparency").

- **OpenAI → Claude masquerade.** AWS Bedrock has no OpenAI models yet, so the
  gateway translates `gpt-*` / `o*-*` model strings to Claude Sonnet 4.6 today.
  A pipeline node with `provider: openai` + `model: gpt-4o` therefore runs on
  Claude. When AWS adds OpenAI models to Bedrock, the gateway updates its own
  mapping and the same request routes to real OpenAI — no tracker change.
  `tracker doctor` surfaces this as a note when the masquerade is actually in
  effect (kind `bedrock` + a gateway URL + `OPENAI_API_KEY`, no
  `OPENAI_BASE_URL` override).
- **Synthesized streaming.** The gateway currently synthesizes the SSE stream
  rather than streaming Bedrock tokens live. A gateway-side streaming overhaul
  is in flight; because tracker is a pure SSE consumer and the wire format is
  identical, real streaming lands with no tracker change — the TUI simply
  receives progressively-displayed tokens.
- **`openai-compat` unsupported.** The bedrock gateway has no `/compat`
  endpoint. A `provider: openai-compat` node under `TRACKER_GATEWAY_KIND=bedrock`
  fails closed: adapter construction returns a wrapped
  `tracker.ErrGatewayRouteRefused` rather than silently falling back to the SDK
  default (which would leak the gateway token to a public host). Use one of the
  three supported providers, or set `OPENAI_COMPAT_BASE_URL` explicitly to
  bypass the gateway for that provider.

## Authentication

Both kinds reuse the conventional provider key env vars, but the *value* differs
from a direct-API setup.

| Provider | Env var |
|----------|---------|
| `anthropic` | `ANTHROPIC_API_KEY` |
| `openai` | `OPENAI_API_KEY` |
| `gemini` | `GEMINI_API_KEY` |
| `openai-compat` | `OPENAI_COMPAT_API_KEY` |

Under the `bedrock` kind the value is a
[Cloudflare AI Gateway](https://developers.cloudflare.com/ai-gateway/) token,
not a provider key — the Worker forwards it as `cf-aig-authorization`. The same
token value goes in every one of the three vars; only the header differs per
SDK. A real Anthropic/OpenAI/Google key is rejected by the gateway, which
surfaces as a 401 (see Troubleshooting).

Tokens for a 2389-operated Worker are provisioned through the 2389 Cloudflare
account.

## Verifying the gateway is actually in use

Tracker does not echo the resolved base URL — neither `tracker doctor` nor the
activity log reports it; both show only the provider name and key status. So
there is no in-tool way to confirm routing directly. A reliable check sequence:

1. **Confirm the env vars are set in the shell tracker will run in.**
   `tracker doctor` validates the key *through the configured base URL* (its
   auth probe goes to the gateway), so a passing doctor check is indirect
   evidence the gateway answered — but it will not print the URL back.
2. **Run one agent node against a trivial pipeline**, then check the
   [Cloudflare AI Gateway dashboard](https://dash.cloudflare.com/?to=/:account/ai/ai-gateway)
   request log for a matching request. That is the authoritative confirmation
   that traffic flowed through the gateway.
3. **On failure, run `tracker diagnose`.** It surfaces the provider error body,
   which carries gateway-specific phrasing (e.g. "Bedrock model not found")
   that distinguishes a gateway-routed failure from a direct-API one.

## Request limits (bedrock kind)

These are properties of the gateway Worker and its Bedrock upstream, not of
tracker, and they are not enforced or detected tracker-side — a run hits them as
an ordinary provider error. As documented by the
[gateway README](https://github.com/2389-research/gateway#known-limitations);
re-check it before relying on any specific number, since these move upstream.

- **Per-request Bedrock timeout** (~30 s at the time of writing). Long agent
  turns with large prompts can hit it; split the work across more turns or
  reduce context.
- **Request-body size limit** (~1 MB). Large transcripts or attachments are
  rejected before reaching Bedrock.
- **Base64-only images.** HTTP image URLs are treated as text, not fetched.
- **No `n > 1`.** Tracker never asks for multiple completions, but a downstream
  integration embedding tracker might.
- **No Bedrock-specific features** (guardrails and similar) — only what fits the
  Anthropic/OpenAI/Gemini SDK shape is exposed.

## Cost accounting through a gateway

`UsageSummary`, `llm.TokenTracker`, and the `--max-cost` budget guard all work
normally: the gateway returns standard token counts in each SDK's native
response shape. Two things to keep in mind when reading the numbers:

- **The recorded provider is the API surface, not the platform.** Usage lands
  under `anthropic` / `openai` / `gemini`, never "bedrock", because that is the
  API tracker spoke.
- **Cost is priced from the model string, not from your AWS invoice.** Dollar
  amounts come from `dippin-lang/pricing` list prices for the requested model
  (see CLAUDE.md, Architecture Gotchas → "Cost governance"), so they will not
  match a Bedrock bill. Under the OpenAI → Claude masquerade the gap is wider
  still: a `provider: openai` + `model: gpt-4o` node is priced as GPT-4o while
  the tokens were actually served by Claude Sonnet. Treat gateway-run costs as a
  budget-guard signal, not as accounting.

## Troubleshooting

**401 / auth failures.** The key is a real provider key. The `bedrock` kind
needs a Cloudflare AI Gateway token (see Authentication).

**A `provider: openai-compat` node fails before sending anything.** Expected —
the bedrock kind refuses to route `openai-compat` (see Caveats). The error wraps
`tracker.ErrGatewayRouteRefused`. Move the node to one of the three supported
providers, or set `OPENAI_COMPAT_BASE_URL` to bypass the gateway for it.

**A `provider: openai` node returns Claude-shaped output.** Also expected — the
masquerade (see Caveats). `tracker doctor` flags it when it is in effect.

**Timeouts around 30 s.** The Bedrock per-request limit, not a tracker timeout.
Reduce prompt size or break the work into more turns.

## Related

- [`./llm.md`](./llm.md#base-url-resolution) — base-URL resolution order and the
  provider adapter wiring.
- [`../../tracker.go`](../../tracker.go) — `GatewayKind`, `gatewaySuffix`,
  `resolveProviderBaseURLWithGateway`, `ResolveProviderBaseURLStrict`,
  `ErrGatewayRouteRefused`.
- `tracker doctor` — preflight gateway routing notes (the "Gateway Routing"
  check).
