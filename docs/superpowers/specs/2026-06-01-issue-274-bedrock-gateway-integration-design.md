# Bedrock-gateway integration — design spec (issue #274)

## Problem

`https://bedrock-gateway.2389-research-inc.workers.dev` is a Cloudflare Worker
that accepts native OpenAI/Anthropic/Gemini SDK requests and translates them to
AWS Bedrock Converse. It lets us run tracker pipelines against Bedrock-backed
models using existing SDKs, with one CF AIG token instead of per-provider
credentials.

Tracker's current routing (`tracker.ResolveProviderBaseURL`) is hard-coded for
**Cloudflare AI Gateway** path conventions:

```
TRACKER_GATEWAY_URL=https://gateway.ai.cloudflare.com/v1/<acct>/<id>
  → /anthropic, /openai, /google-ai-studio, /compat
```

The bedrock gateway uses **native SDK path conventions** instead:

| Provider | Path the SDK calls | Base URL tracker must hand the SDK |
|----------|-------------------|-----------------------------------|
| Anthropic | `/v1/messages` | `https://bedrock-gateway.../` (SDK adds `/v1/messages`) |
| OpenAI | `/v1/chat/completions`, `/v1/responses` | `https://bedrock-gateway.../v1` (SDK adds the rest) |
| Gemini | `/v1/models/{model}:generateContent` | `https://bedrock-gateway.../v1` |
| openai-compat | n/a | unsupported on bedrock gateway |

So `TRACKER_GATEWAY_URL` as-is is incompatible with the bedrock gateway.

## Goals

1. Operators can switch a tracker run to the bedrock gateway with one flag (or
   one env var) plus a CF AIG token. No code changes needed in their `.dip`.
2. Existing CF AIG users see zero behavior change.
3. Tracker stays a pure passthrough on two upstream-driven events:
   - Bedrock adds real OpenAI model support → gateway updates its mapping;
     tracker emits the same request shape it does today.
   - Real Bedrock streaming lands in the gateway → tracker's TUI just gets
     progressively-displayed tokens, no code change.
4. Operators get a clear preflight warning about the **OpenAI → Claude
   masquerade** (today, `gpt-4o` silently routes to Claude Sonnet 4.6 on the
   bedrock gateway). The masquerade is the highest-friction "wtf" bug a new
   tracker+bedrock user would file.

## Non-goals

- A native `provider: bedrock` adapter — overkill; the gateway translates.
- Hardcoding the bedrock-gateway URL as a tracker default — couples tracker
  to a 2389-specific URL we don't own forever.
- Runtime warnings every session — noisy; doctor preflight covers the same
  risk without log spam.
- Reachability probes against the gateway — premature optimization until
  users actually file "can't reach gateway" support load.
- Streaming overhaul (issue #274 may also touch this) — out of scope for
  this PR; the gateway's streaming-overhaul work is upstream.

## Decisions

### D1. Generalize over parallel — `TRACKER_GATEWAY_KIND`

Reject adding a parallel `TRACKER_BEDROCK_GATEWAY_URL` env var. Two parallel
gateway concepts is a maintenance trap. Instead, add one small enum that
governs the suffix map for the existing `TRACKER_GATEWAY_URL`:

| `TRACKER_GATEWAY_KIND` | Default | Anthropic | OpenAI | Gemini | openai-compat |
|------------------------|---------|-----------|--------|--------|---------------|
| `cf-aig` (default)     | ✓       | `/anthropic` | `/openai` | `/google-ai-studio` | `/compat` |
| `bedrock`              |         | `""`      | `/v1`  | `/v1`  | refuse-route  |

Default is `cf-aig` so existing setups don't change. New flag
`--gateway-kind` pairs with the existing `--gateway-url`.

### D2. Precedence — per-provider env vars still win

Order of resolution stays:

1. `<PROVIDER>_BASE_URL` (per-provider, explicit) — wins unconditionally.
2. `TRACKER_GATEWAY_URL` + `TRACKER_GATEWAY_KIND`-dependent suffix — fallback.
3. Empty (SDK default) — last resort.

This preserves the existing "per-provider env var as surgical override"
behavior. Users debugging a specific provider can still set
`ANTHROPIC_BASE_URL=...` and have it win.

### D3. openai-compat + bedrock is an error

The bedrock gateway has no `/compat` equivalent. If a workflow specifies
`provider: openai-compat` AND `TRACKER_GATEWAY_KIND=bedrock` is in effect,
tracker refuses at `ResolveProviderBaseURL` (returns error / logs a clear
mismatch). Better fail-fast than silent 404.

### D4. Doctor surfaces the masquerade caveat — not runtime

`tracker doctor` is the right home for the OpenAI→Claude warning. It runs
once at setup time, doesn't repeat per-session, and operators expect
diagnostic output there.

Doctor logic: when `KIND=bedrock` AND any workflow node uses
`provider: openai` with a model matching `^(gpt-|o\d-)`, emit a clear note:
"this model routes to Claude Sonnet 4.6 today via the bedrock gateway;
when AWS adds OpenAI model support to Bedrock the gateway will route to
real OpenAI without tracker changes."

### D5. Doctor catches gateway+per-provider conflicts

When both `TRACKER_GATEWAY_URL` and one or more `<PROVIDER>_BASE_URL` are
set, doctor logs the precedence (the per-provider URL wins) so the
operator isn't surprised. Not an error — just visibility.

### D6. Tracker is transparent to upstream events

| Event | Tracker action |
|-------|----------------|
| AWS adds OpenAI on Bedrock | nothing — gateway updates model mapping |
| Real streaming lands in gateway | nothing — SSE wire is identical |
| Gateway URL changes | operators update their `TRACKER_GATEWAY_URL` |

Confirmed by the routing-internals reviewer: tracker is a pure SSE
consumer and never inspects model strings beyond passing them through.

## Architecture

```
.dip workflow
   ↓ (provider: anthropic|openai|gemini)
codergen handler
   ↓ (buildLLMClient)
tracker.ResolveProviderBaseURL(provider)
   ├─ check <PROVIDER>_BASE_URL → return it
   ├─ TRACKER_GATEWAY_URL empty → return ""
   ├─ kind := TRACKER_GATEWAY_KIND  (default cf-aig)
   ├─ KIND=cf-aig:    return url + cfAIGSuffix[provider]
   └─ KIND=bedrock:   return url + bedrockSuffix[provider]
                       (refuse if provider == openai-compat)
   ↓
provider SDK client
   ↓
gateway (cf-aig OR bedrock-gateway)
```

Existing CLI flag chain unchanged: `--gateway-url` sets `TRACKER_GATEWAY_URL`
before `buildLLMClient`. New CLI flag `--gateway-kind` sets
`TRACKER_GATEWAY_KIND` the same way. `Config.GatewayKind` library option
mirrors `Config.GatewayURL`.

## Tightly-scoped delivery — three issues

The work decomposes into three independent PRs, each shippable on its own:

1. **Issue A: routing generalization** — `TRACKER_GATEWAY_KIND` env var +
   `--gateway-kind` flag + suffix dispatch in `ResolveProviderBaseURL`.
   The core. ~80 LOC + tests.
2. **Issue B: doctor preflight notes** — masquerade detection (D4) +
   conflict detection (D5). ~40 LOC + tests.
3. **Issue C: docs** — CLAUDE.md gotcha + CHANGELOG entry +
   `docs/architecture/bedrock-gateway.md` (or site page). No code.

Each issue links back to umbrella issue #274. They land in any order
but Issue A blocks B (B reads `KIND`).

## Out of scope (deferred per YAGNI)

- Hardcoded default bedrock-gateway URL.
- Runtime per-session warning logs about masquerade.
- Reachability probe in doctor.
- Native `provider: bedrock` adapter type.
- Streaming wire changes (real streaming is gateway-side; tracker
  inherits transparently when it lands).
- Auth flow changes — CF AIG token already works as `*_API_KEY` for all
  three providers; no new credential machinery needed.

## Verification gates per issue

- `go build ./...` clean on Linux + `GOOS=darwin GOARCH=amd64`.
- `go test ./... -short` green.
- `dippin doctor examples/*.dip` unchanged (no IR-level changes).
- Manual smoke: a `.dip` with `provider: anthropic` + `model: claude-sonnet-4-6`
  + `TRACKER_GATEWAY_KIND=bedrock` + `TRACKER_GATEWAY_URL=<bedrock-gateway>` +
  `ANTHROPIC_API_KEY=<cf-aig-token>` runs and returns a completion.
- Manual smoke: same with `cf-aig` kind against an actual CF AI Gateway URL
  — confirms back-compat.

## Release sequence

1. Issue A lands first (routing generalization is the prerequisite).
2. Issue B (doctor preflight) lands second.
3. Issue C (docs) can land in parallel with B; it's content-only.
4. CHANGELOG `[Unreleased]` accumulates entries across the three PRs.
5. The bundle ships in the next minor release after #274 is closed.
