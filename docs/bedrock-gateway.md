# Moved: Bedrock Gateway setup

This guide has moved to
[`docs/architecture/bedrock-gateway.md`](architecture/bedrock-gateway.md),
which is the single authoritative operator guide for gateway routing —
both the Cloudflare AI Gateway (`cf-aig`, default) and the 2389
bedrock-gateway Worker (`bedrock`) kinds.

This page is kept only so existing links do not 404.

## Why the move

The instructions that used to live here predated the
`--gateway-kind` / `TRACKER_GATEWAY_KIND` dispatch and described a
provider matrix that no longer matches the runtime. Following them today
produces behavior opposite to what they promised. Concretely, against a
bedrock gateway:

| This page used to say | What tracker actually does |
|---|---|
| Leave the gateway kind unset (Cloudflare suffixes `/anthropic`, `/compat`, …) | The bedrock Worker needs `--gateway-kind bedrock`, which uses **native** suffixes — `""` for Anthropic, `/v1` for OpenAI and Gemini |
| `provider: openai-compat` works | It **refuses to route** and fails closed with `tracker.ErrGatewayRouteRefused` — there is no `/compat` endpoint |
| Avoid `provider: openai` | It **works**, but under the OpenAI → Claude masquerade: `gpt-*` model strings are served by Claude Sonnet today |

Because this is credential and routing guidance rather than cosmetic
prose, the contradiction was tracked as SIFT-SUB-16-01's sibling finding
SIFT-SUB-16-02 (#610) and resolved by making the architecture guide the
sole authority. The still-accurate material that was unique to this page
— authentication, verification steps, gateway request limits, cost-
accounting caveats, and troubleshooting — was merged into it.

The routing rules themselves live in
[`../tracker.go`](../tracker.go) (`gatewaySuffix`,
`resolveProviderBaseURLWithGateway`, `ErrGatewayRouteRefused`).
