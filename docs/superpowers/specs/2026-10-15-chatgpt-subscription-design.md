# ChatGPT subscription for the `openai` provider (Codex OAuth) — design

- **Date:** 2026-10-15
- **Status:** draft for Doctor Biz's review
- **Branch:** `feat/chatgpt-subscription`
- **Research:** [2026-10-15-chatgpt-subscription-research.md](2026-10-15-chatgpt-subscription-research.md).
  It read openai/codex at `rust-v0.162.1` (commit `092d3acd`, the version
  installed here as `codex-cli 0.162.1`) and checked it against pi
  (`earendil-works/pi` at `4954d760`). Citations such as `server.rs:77` point
  into openai/codex under `codex-rs/` unless marked pi; the research file has
  the full paths.

## Goal

The request was "let's add a codex login flow to the setup", narrowed in
conversation: "we don't want to use the codex acp", "we want to use openai
models via subscription", "using codex oauth login".

Tracker's native backend runs OpenAI models billed to the user's ChatGPT
subscription instead of an API key. The user signs in once from
`tracker setup` with Codex's OAuth device-code flow. From then on every
`provider: openai` node goes through ChatGPT's Codex backend. Nothing runs
through `codex-acp` or a `codex` subprocess.

Success: on a machine with no `OPENAI_API_KEY`, the user picks OpenAI → ChatGPT
subscription in `tracker setup` and finishes the device code on a phone.
`tracker doctor` then reports the sign-in, and a pipeline with an openai node
runs, its summary showing `openai-subscription` tokens at $0.

## Decisions (2026-10-15)

Answers are quoted as given.

| # | Question | Doctor Biz's answer |
|---|---|---|
| D1 | Who runs the agent loop on the subscription? | "Tracker native (Recommended)" — not `codex-acp`, not a `codex exec` backend |
| D2 | How does a node choose the subscription over API-key billing? | "Login mode of openai (Recommended)": `provider: openai` keeps its name, and a switch in the config `.env` routes it through the ChatGPT login |
| D3 | OpenAI's stated policy on third-party use | "isn't tihs a pure open source option? ", then "Yes, build it (Recommended)": an open-source client, one person's own login, identifying itself as tracker |
| D4 | Which sign-in does setup offer? | "Device code only (Recommended)" |
| D5 | Where does sign-in live in setup? | "Auth choice on OpenAI (Recommended)" |
| D6 | How do runs report cost for subscription calls? | "$0, tokens counted"; after learning that claude-code subscription runs report API-equivalent dollars, "$0 for ChatGPT only" |
| D7 | Architecture | "A: mode in openai adapter (Recommended)" |

## Policy

The design reuses the Codex CLI's OAuth client (`app_EMoamEEZ73f0CkXaXp7hrann`)
and its private backend (`https://chatgpt.com/backend-api/codex`). OpenAI's
Codex lead told a developer building a client like this: "Sorry, but this is
not an approved use of Sign in With ChatGPT. We support pure OSS clients and
others we have partnerships with, but you need to reach out and talk to us for
that." (research §I; the quote comes from a search index, because X refused a
direct fetch). Tracker is public and MIT-licensed, so we treat it as a pure
open-source client (D3), on these terms:

- The login belongs to one person, on their own machine. The docs say so. The
  feature is not for serving other people, such as trackerbot in a shared Slack
  or a hosted runner signed in to one account.
- Requests say who sends them: `originator: tracker` and a tracker
  `User-Agent`, never Codex's values.
- If the backend refuses a non-Codex originator, implementation stops and the
  decision goes back to Doctor Biz.

## Design

### Overview

```
tracker setup --device code--> auth.openai.com --tokens--> ~/.config/tracker/chatgpt-auth.json
                                                                   |
TRACKER_OPENAI_AUTH=chatgpt --> buildClient --> openai adapter in ChatGPT mode
                                                   |  TokenSource from internal/chatgptauth
                                                   |  (refresh, mutex + flock)
                                                   v
                                  POST chatgpt.com/backend-api/codex/responses (SSE)
```

| Unit | Change |
|---|---|
| `internal/chatgptauth` (new) | device-code login, token file, claims, refresh with locking; exposes a `TokenSource` |
| `llm/openai` | `WithChatGPTAuth(TokenSource)` option: URL, headers, body reshape, streaming `Complete`, error mapping |
| `tracker_client.go`, `llm/client.go` | register openai from the token source when the switch says `chatgpt` |
| `internal/envpolicy` | one row: `TRACKER_OPENAI_AUTH` |
| `cmd/tracker` setup wizard | Auth choice on the OpenAI page; device-code page |
| cost path (`llm` pricing, token tracker, session stats) | `openai-subscription` label priced at $0 |
| doctor, `tracker version` | report the sign-in |

### 1. Sign-in and the token file (`internal/chatgptauth`)

**Device-code sign-in** (`login/src/device_code_auth.rs`):

1. `POST https://auth.openai.com/api/accounts/deviceauth/usercode` with JSON
   `{"client_id": "app_EMoamEEZ73f0CkXaXp7hrann"}` returns `device_auth_id`,
   `user_code` (also spelled `usercode`) and `interval`, as a string or a
   number (`:27-34`, `:63-97`). A 404 means device-code sign-in is not enabled
   (`:83-88`). Setup says so and stops; there is no browser fallback (D4).
2. Show `https://auth.openai.com/codex/device` and the code (`:165-180`), with
   a warning like Codex's: continue only if you started this sign-in in
   tracker.
3. Poll `POST https://auth.openai.com/api/accounts/deviceauth/token` with JSON
   `{"device_auth_id", "user_code"}` every `interval` seconds. 403 and 404 mean
   still pending; any other non-200 status fails. Stop after 15 minutes
   (`:100-147`, `:108`) or when the user cancels.
4. A 200 carries `authorization_code`, `code_challenge` and `code_verifier`;
   the server, not the client, generates the PKCE pair (`:55-60`). Exchange it
   at `POST https://auth.openai.com/oauth/token`, form-encoded:
   `grant_type=authorization_code`, `client_id`, `code`,
   `redirect_uri=https://auth.openai.com/deviceauth/callback`, `code_verifier`
   (`:182-236`; `login/src/oauth/client.rs:58-74`). The response holds
   `id_token`, `access_token` and `refresh_token` (`login/src/server.rs:698-703`).
   The device flow skips Codex's id_token-to-API-key exchange
   (`device_code_auth.rs:228`), and so does tracker.

**Identity.** Decode the id_token's `https://api.openai.com/auth` claims
without checking the signature, as Codex does (`login/src/token_data.rs:129-140`).
Take `chatgpt_account_id` and `chatgpt_plan_type` from them
(`token_data.rs:77-98, 174-198`), capturing the account ID at sign-in the way
Codex does (`server.rs:856-861`). The account ID goes into a request header and
is never printed. Setup and doctor show the plan type. Expiry comes from the
access token's `exp` claim (`token_data.rs:142-147`).

**Token file.**

- Path: `$XDG_CONFIG_HOME/tracker/chatgpt-auth.json`, by default under
  `~/.config/tracker/`. It resolves the same way as the config `.env`, which
  ignores a relative `XDG_CONFIG_HOME`. The resolver moves out of
  `cmd/tracker/config_env.go` into a small library package that both use, so
  there is one resolver.
- Fields: `id_token`, `access_token`, `refresh_token`, `account_id`,
  `last_refresh`.
- Write it atomically (a temp file at mode 0600 in the same directory, then
  rename) in a 0700 directory.
- Refuse to read a symlink (`O_NOFOLLOW`) or a file whose group or other bits
  allow reading or writing, with a fix hint (`chmod 600`). On Windows, match
  the config `.env` loader (`cmd/tracker/envfiles_windows.go`): there is no
  `O_NOFOLLOW`, so an `Lstat` symlink check is the only guard, and mode bits
  aren't checked.
- Tokens never enter the process environment, a log line, an event or an
  error string.
- Keep it separate from `~/.codex/auth.json`: tracker has its own sign-in and
  its own chain of refresh tokens. A refresh token works once
  (`refresh_token_reused`, `login/src/auth/manager.rs:206-211, 1680-1708`),
  so two clients sharing a chain would sign each other out.

**Refresh** (`manager.rs:1600-1648, 1710-1715, 3001-3023`):

- Before handing out an access token, refresh if its `exp` falls within 5
  minutes (`manager.rs:204`). If `exp` won't parse, refresh when
  `last_refresh` is more than 8 days old (`manager.rs:203`).
- After a 401 from the backend, refresh once and retry the request once.
- `POST https://auth.openai.com/oauth/token` with JSON
  `{"grant_type":"refresh_token","client_id":…,"refresh_token":…}`. Store
  whichever of `id_token`, `access_token` and `refresh_token` come back, and
  keep the old refresh token if no new one does.
- One refresh at a time: a `sync.Mutex` inside the process, plus an exclusive
  `flock` on `chatgpt-auth.lock`, next to the token file, across processes.
  After taking the lock, re-read the token file; if another process refreshed
  first, use its token. On Windows, lock in-process only (verify at
  implementation; tracker already splits `_unix.go` and `_windows.go` files).
- A permanent failure stops the run: error code `refresh_token_expired`,
  `refresh_token_reused` or `refresh_token_invalidated`, any 401, or a 400
  `invalid_grant` (`manager.rs:1659-1671`). The message reads "ChatGPT sign-in
  expired or was revoked (<reason>); run `tracker setup` to sign in again."
  Network errors and 5xx are transient and may be retried.

### 2. The switch, building the client, requests and errors

**Switch: `TRACKER_OPENAI_AUTH`.**

- Values are `chatgpt` and `api-key`. Unset means `api-key`, which is today's
  behavior. Any other value is a configuration error that names the two valid
  values.
- Registered in `internal/envpolicy` with `Purpose: NetworkDestination` and
  `Source: ConfigEnv`. The shell or `~/.config/tracker/.env` may set it; a
  project `<workdir>/.env` may not (#659).
- `tracker setup` writes it. Setup's write allowlist, which comes from the env
  registry, gains this one name.
- Under `chatgpt`, openai uses the token file even when `OPENAI_API_KEY` is
  set. Doctor notes that the key goes unused for openai.

**Building the client** (`tracker_client.go` `buildClient`, `llm/client.go`):

- Under `chatgpt`, openai registers from the token source instead of from
  `OPENAI_API_KEY`, so a machine with only the ChatGPT login and no API keys
  can build a client. Today `NewClientFromEnv` registers a provider only when
  it finds that provider's `*_API_KEY` (`llm/client.go` `tryBuildProvider`).
- If the user isn't signed in, openai still registers, and its first call fails
  with "openai: TRACKER_OPENAI_AUTH=chatgpt but not signed in; run
  `tracker setup`". An Anthropic-only pipeline still runs, and doctor flags the
  problem first.
- The "no providers configured" error mentions the ChatGPT sign-in next to the
  API keys.
- Library callers (`tracker.NewLLMClient`, `tracker.Run`) take the same path.
- The provider stays `openai`, so pipelines need no edits and dippin-lang no
  change.

**Fixed destination.** In ChatGPT mode, `OPENAI_BASE_URL`,
`TRACKER_GATEWAY_URL`, `--gateway-url` and `--gateway-kind` do not apply to
openai. The access token goes only to `https://chatgpt.com/backend-api/codex`
and the refresh token only to `https://auth.openai.com`, so a planted or stale
base URL never receives the bearer. Doctor warns when one of those settings is
present but unused for openai. Tests reach an `httptest` server through Go
options on the adapter and the token source, never through env vars.

**Requests** (`llm/openai`, new option `WithChatGPTAuth(TokenSource)`):

- `POST https://chatgpt.com/backend-api/codex/responses`
  (`model-provider-info/src/lib.rs:80`; `codex-api/src/endpoint/responses.rs:67`).
- Headers:
  - `Authorization: Bearer <access_token>`
  - `ChatGPT-Account-ID: <account_id>` (`model-provider/src/bearer_auth_provider.rs:31-46`)
  - `originator: tracker`
  - `User-Agent: tracker/<version> (<GOOS>; <GOARCH>)`
  - `session-id: <uuid>`, one per adapter instance (`codex-api/src/requests/headers.rs:5-13`)
  - `Content-Type: application/json`

  Not sent: Codex's `originator: codex_cli_rs` and user agent (D3),
  `X-OpenAI-Fedramp` (FedRAMP workspaces only), `thread-id`, the `x-codex-*`
  headers, and `OpenAI-Beta`, which Codex's HTTP path doesn't send (research,
  "Disagreements").
- Body: run the usual `translateRequest`, then reshape the JSON in one
  separate function, which keeps the complexity ratchet green:
  - set `store: false`; the backend rejects `true` with "Store must be set to
    false" (pi `openai-codex-responses.ts:1520-1521`; Codex
    `core/src/client.rs:1003`);
  - set `stream: true` (`client.rs:1004`) and
    `include: ["reasoning.encrypted_content"]` (`client.rs:961, 1006`);
  - set `prompt_cache_key` to the session id; Codex always sends one
    (`client.rs:581-593`);
  - delete `max_output_tokens`, `temperature`, `top_p` and `stop`, none of
    which exist in the official request struct
    (`codex-api/src/common.rs:279-304`). A node's max-tokens setting therefore
    does nothing in this mode;
  - keep `instructions`, which pi sends (`openai-codex-responses.ts:558`).
    Codex instead sends the system prompt as a `developer` message at
    `input[0]` (`core/src/context/base_instructions.rs:26-46`). If the backend
    rejects `instructions`, switch to Codex's shape (verify at
    implementation).
- `Complete` streams and folds the events with `llm.NewStreamAccumulator`,
  because the backend has no non-streaming mode (`client.rs:1004`).
- The SSE events match the public Responses API, plus `response.metadata`
  (`codex-api/src/sse/responses.rs:211-253, 347-489`), which the parser must
  ignore (verify).
- Encrypted reasoning items come back in the stream. Tracker doesn't replay
  them into later turns, the same as its API-key path today.

**Errors** (`codex-api/src/api_bridge.rs:181-235, 340-352`):

- 401: refresh once and retry once. A second 401 stops the run with the
  sign-in-again message.
- 429 with `error.type == "usage_limit_reached"` returns a non-retryable
  error: "openai: ChatGPT usage limit reached (<plan_type> plan), resets at
  <resets_at as RFC3339>". That text satisfies the existing `llm.IsUsageLimit`
  and `llm.UsageLimitResetAt` (`llm/usage_limit.go`), so `recoverablePause`
  (`pipeline/handlers/codergen_errors.go`) stops the run in the resumable
  `paused_billing` state with `ResumeAfter` set (#590). The retry middleware
  must not retry this error first; check the error type at implementation.
- 429 with `error.type == "usage_not_included"`: non-retryable, "openai: your
  ChatGPT plan does not include Codex usage".
- 429 with `insufficient_quota` or the other quota codes goes down the existing
  billing path (`llm.IsBillingError`).
- Other 429s and a 503 `server_is_overloaded` stay retryable, as today.
- A model the plan doesn't serve: non-retryable, carrying the server's message
  (the exact body shape needs checking).
- No error text includes a token.

### 3. Cost, setup wizard, doctor, version and docs

**Cost (D6).**

- Calls made in ChatGPT mode carry the provider label `openai-subscription`
  wherever tracker adds up usage by provider: the `llm.TokenTracker` buckets,
  session stats, `Trace.AggregateUsage`, the CLI summary, the TUI cost badge,
  NDJSON `cost_updated` and `budget_exceeded` events, and diagnose.
- Pricing returns $0 for that label by design and skips the unknown-model
  warning. Token counts and token ceilings work as for any provider.
- The first wiring to try (verify at implementation): the adapter stays
  registered and routed as `openai`, but the response's provider label, which
  `llm.Client` sets from `adapter.Name()` today (`llm/client.go:247, 304`),
  comes from an optional adapter method, and the native session's stats take
  the label from the responses.
- No dollar ceiling applies to subscription calls. That covers `--max-cost`,
  `Config.Budget`, `.dip` `defaults: max_cost_cents`, and per-node
  `params: max_cost_usd`, such as `build_product`'s ReviewCodex cap of $4.00
  (#353). When ChatGPT mode is on and any such ceiling is set, the run prints
  one warning at the start saying subscription calls don't count toward it. A
  runaway node then spends plan usage until the usage limit pauses the run.
  `tracker estimate` prices openai nodes at $0 and gives the same warning.
- claude-code subscription runs keep reporting API-equivalent dollars (D6).

**Setup wizard (D5), `cmd/tracker`.**

- The OpenAI page gains an **Auth** row:
  `(•) ChatGPT subscription  ( ) API key`. It starts on ChatGPT subscription
  when the switch says `chatgpt`, otherwise on API key.
  - API key shows today's API-key and base-URL fields, unchanged.
  - ChatGPT subscription shows a status line ("plus plan · refreshed 2h ago"
    or "not signed in") and "enter: sign in" (or "sign in again").
- The sign-in page shows the URL, the code, "expires in 15 min", a spinner,
  and the line "Signs in through Codex's OAuth client — for your own ChatGPT
  account on your own machine." The device flow runs in a `tea.Cmd` with a
  context that esc cancels.
  - On success: write the token file, set `TRACKER_OPENAI_AUTH=chatgpt` in the
    config `.env`, show the plan, and go on to the next provider.
  - On failure: show the error on the OpenAI page; the user can retry or pick
    API key.
- Leaving the page on API key writes `TRACKER_OPENAI_AUTH=api-key` only when
  the switch was `chatgpt`; otherwise setup leaves the switch unset, so
  API-key users get no new line in their config. Setup's merge never deletes a
  key, so `api-key` is how it turns the mode off. The token file stays;
  deleting it signs out. This change adds no `tracker logout`.
- The new screens get their own file, so `setup_model.go` (405 lines) stays
  under the complexity ratchet.

**Doctor** (`tracker_doctor_providers.go`).

- Offline checks: the switch's value; the token file's existence, mode and
  symlink status; and token expiry. The detail line reads "openai: ChatGPT
  subscription (plus plan), signed in". It never prints the account ID or
  email.
- The live probe (`--probe`, on by default, `cmd/tracker/flags.go:75`)
  refreshes the access token instead of sending a generation request. That
  proves the sign-in still works, spends no plan usage, and needs no guess at
  which models the plan serves.
- Errors, each with a `tracker setup` hint: the switch is on but nobody is
  signed in; refresh failed permanently; the file is unsafe.
- Warnings: `OPENAI_BASE_URL` or a gateway is set but unused for openai;
  `OPENAI_API_KEY` is set but unused for openai; a dollar ceiling won't count
  subscription calls.

**`tracker version`** lists `openai (ChatGPT subscription)` when the switch is
on and the token file exists.

**Docs, in the same change:**

- CHANGELOG `[Unreleased]`, under Added.
- README provider setup.
- `site/content/cli.html`: `tracker setup` and the new variable.
- `docs/architecture/env-policy.md`: the new row.
- `docs/architecture/backends.md`: native auth is API keys, or the ChatGPT
  subscription for openai.
- A `gotchas.md` entry: refresh tokens are single-use; never share auth with
  the Codex CLI; personal use only; device code only.

### 4. Testing and proof

**Unit tests:**

- claims parsing: account ID, plan, `exp`;
- the token file: atomic 0600 write, refused symlink, refused group or other
  bits, round trip;
- the refresh decision: the 5-minute window and the 8-day fallback;
- the request reshape: golden JSON for body and headers;
- error mapping: `usage_limit_reached` satisfies `llm.IsUsageLimit` and
  `llm.UsageLimitResetAt` and isn't retried; `usage_not_included`; each
  permanent refresh failure;
- switch parsing, including an invalid value;
- `openai-subscription` priced at $0 with tokens counted, and the
  dollar-ceiling warning;
- setup states (auth choice, waiting, success, failure, esc), with the login
  function injected.

**Integration tests**, against an `httptest` server that plays the documented
endpoints so tracker's real client code runs end to end in-process:

- device-code login: pending, pending, code, exchange, file written; the 404
  "not enabled" case; the timeout, with a short timeout injected;
- `Stream` and `Complete` in ChatGPT mode, with the request shape checked on
  the server side and a reply that includes `response.metadata`;
- 401, then refresh, then success; each permanent refresh failure;
- a refresh race. Goroutines and two real processes (the test binary
  re-executed) refresh at once. The server must see exactly one refresh, and
  every caller must end with the new token. Per #658, witness both sides with
  `internal/testutil` (`Witness`, `Overlap`, `Eventually`), state the bounded
  fork count, and put each adversary's exit code and stderr in every failure
  message.

**Security invariants, as tests:**

- the loader skips `TRACKER_OPENAI_AUTH` from a project `.env`;
- in ChatGPT mode, with `OPENAI_BASE_URL` and `TRACKER_GATEWAY_URL` pointed at
  a trap server, the trap receives no request;
- no token value appears in any error string from the auth package or the
  adapter.

**End-to-end, with a real account.** These are opt-in, and Doctor Biz does the
sign-in, the one physical-world step:

1. `tracker setup` → OpenAI → ChatGPT subscription, finishing the device code
   on a phone.
2. `tracker doctor` passes, including the refresh probe.
3. A small real pipeline, one openai agent node and one tool node, runs on a
   model the plan serves; the summary shows `openai-subscription` tokens at $0.
4. A Go test gated on `TRACKER_E2E_CHATGPT=1` sends one real `Complete` and
   one real `Stream`. Without the variable it skips and says why.

The evidence (commands and output, no tokens) goes in the PR.

**Gates:** run `make ci` by hand before pushing; it is the repo's canonical
check. Jail code is untouched, so the Landlock job isn't needed.

## Threat model

| Asset or path | Risk | Control |
|---|---|---|
| Token file | any process running as the user can read it, agent tools included | the same exposure as `~/.codex/auth.json` and the config `.env`; the `writable_paths` jail limits writes, not reads. Mode 0600, symlinks refused, documented |
| Bearer token in transit | sent to a planted or stale base URL | fixed destination: base-URL and gateway settings never apply in ChatGPT mode (invariant test) |
| The switch | a planted project `.env` changes billing and destination | `ConfigEnv` source; the loader skips it from a project `.env` (invariant test) |
| Tokens in output | leak through logs, events, errors, diagnose or bundles | tokens never enter the environment, events or errors (invariant test) |
| Refresh-token chain | concurrent refreshes use it up, since each token works once | mutex, `flock`, and a re-read after taking the lock (race test) |
| The ChatGPT account | OpenAI objects to this use | D3 terms: personal login, honest originator, docs; stop if the backend refuses `originator: tracker` |

## Open items (verify at implementation)

1. Whether the backend accepts `originator: tracker` and tracker's User-Agent.
   If not, stop and take it back to Doctor Biz (D3).
   **Resolved 2026-10-16**: accepted. Run 1 of
   `TestChatGPTSubscriptionLive` (2026-10-16 08:22 CDT), model
   `gpt-6.1-sol`, plan claim `self_serve_business_prolite`: all three
   subtests passed — Complete (system+user) 1.98s, Stream (system+user)
   2.01s, Complete (user only) 1.93s. No token or account id recorded.
2. Whether `instructions` is accepted, or Codex's `developer`-message shape is
   needed.
   **Resolved 2026-10-16**: `instructions` is accepted as sent — no shape
   change needed. Same run as item 1: the Complete-with-system-message
   subtest (which carries `instructions`) passed. A request with no system
   message at all — so no `instructions` field — was also accepted (the
   "Complete with user message only, no instructions" subtest passed too),
   so the backend needs no default when it's absent.
3. The body and status for a model the plan doesn't serve.
   **Resolved 2026-10-16**: body recorded against `gpt-4.1-mini`:
   `{"detail":"The 'gpt-4.1-mini' model is not supported when using Codex
   with a ChatGPT account."}`. Status: recorded in the opt-in
   `TRACKER_E2E_CHATGPT_UNSERVED_MODEL` subtest of
   `TestChatGPTSubscriptionLive` (not captured on the run that recorded the
   body above). `chatgptStatusError` turns a `detail` string into the error
   message instead of the raw JSON body.
4. One `session-id` per adapter or one per agent session, for cache hits.
   **Resolved 2026-10-16**: stays one per adapter. The run in item 1 sent
   all three calls through one adapter (one session id) with no problem.
5. How the `openai-subscription` label threads through session stats and
   traces.
6. File locking on Windows.
7. Whether the retry middleware skips the `usage_limit_reached` error type.

## Out of scope

- Browser sign-in (D4), a `tracker logout` command, and sharing or importing
  `~/.codex/auth.json`.
- A separate provider name (D2) and any dippin-lang change.
- Model discovery through the backend's `/models` endpoint
  (`model-provider/src/models_endpoint.rs:119`). A model the plan doesn't
  serve fails at call time.
- Autopilot's openai default (`gpt-4.1-mini`,
  `pipeline/handlers/autopilot.go:212`) may not be on the plan. In ChatGPT mode
  with openai as the default provider, the user sets the autopilot model
  explicitly, and the docs say so.
- Replaying encrypted reasoning items across turns.
- Any change to how claude-code subscription usage is priced.

## Size

About 1,300 lines of product code and 1,700 lines of tests, plus docs, built
with subagent-driven development from a plan in `docs/superpowers/plans/`.
