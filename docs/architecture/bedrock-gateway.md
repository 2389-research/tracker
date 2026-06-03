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
2. **`TRACKER_GATEWAY_URL` + kind-dependent suffix** — the gateway fallback.
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

Per-provider suffixes under `bedrock` follow native SDK path conventions:

| Provider | Suffix | Why |
|----------|--------|-----|
| `anthropic` | `""` (none) | the Anthropic SDK appends `/v1/messages` itself |
| `openai` | `/v1` | the OpenAI SDK appends `/chat/completions` etc. |
| `gemini` | `/v1` | the Gemini SDK appends `/models/{model}:...` |
| `openai-compat` | — | **refuses to route** (see caveats) |

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

## Related

- [`./llm.md`](./llm.md#base-url-resolution) — base-URL resolution order and the
  provider adapter wiring.
- [`../../tracker.go`](../../tracker.go) — `GatewayKind`, `gatewaySuffix`,
  `resolveProviderBaseURLWithGateway`, `ResolveProviderBaseURLStrict`,
  `ErrGatewayRouteRefused`.
- `tracker doctor` — preflight gateway routing notes (the "Gateway Routing"
  check).
