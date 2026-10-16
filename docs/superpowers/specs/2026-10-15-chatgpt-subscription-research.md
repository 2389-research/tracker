# Codex ChatGPT-OAuth research

Research for [2026-10-15-chatgpt-subscription-design.md](2026-10-15-chatgpt-subscription-design.md),
gathered 2026-10-15 by reading source and public pages only — no request was
sent to auth.openai.com or chatgpt.com.

Sources read:

1. **openai/codex**, tag `rust-v0.162.1`, commit `092d3acd6bec3e3a14bdc7e7a2810ab628ab759d` (tag object `7a078f6bb4ce0dc2a81408b334ce52979d92c603`), read from a shallow clone.
   Matches the locally installed `codex-cli 0.162.1`.
2. **Third-party cross-check**: `badlogic/pi-mono` has moved/renamed — `gh repo view badlogic/pi-mono` resolves to **`earendil-works/pi`** (same project: "AI agent toolkit: unified LLM API, agent loop, TUI, coding agent CLI"). Read from a shallow clone of the default branch at
   commit `4954d760504c11df9b2cbdc040346107b6a8153d` (2026-10-15). The relevant files are `packages/ai/src/auth/oauth/openai-codex.ts` and `packages/ai/src/api/openai-codex-responses.ts`, under provider id `openai-codex` (pi-mono itself labels it "OpenAI Codex (**legacy**)").
3. Official docs/announcements and OpenAI-staff statements, via WebSearch/WebFetch (urls cited inline in section I).

All file:line citations below are at commit `092d3acd6bec3e3a14bdc7e7a2810ab628ab759d` of openai/codex unless marked "pi-mono" or a URL.

---

## A. Browser sign-in (PKCE)

- **Issuer / base URL**: `https://auth.openai.com` — `codex-rs/login/src/server.rs:77` (`DEFAULT_ISSUER`).
- **Authorize endpoint**: `{issuer}/oauth/authorize` — `codex-rs/login/src/server.rs:605`.
- **Query parameters sent** (`build_authorize_url`, `codex-rs/login/src/server.rs:586-618`, via `build_authorization_url` in `codex-rs/login/src/oauth/authorization.rs:21-42`):
  - `response_type=code`
  - `client_id=app_EMoamEEZ73f0CkXaXp7hrann` (see E for the constant; overridable by `CODEX_APP_SERVER_LOGIN_CLIENT_ID`)
  - `redirect_uri=http://127.0.0.1:{port}/auth/callback`
  - `code_challenge=<S256 challenge>`
  - `code_challenge_method=S256`
  - `state=<32 random bytes, base64url-nopad>`
  - `scope=openid profile email offline_access api.connectors.read api.connectors.invoke` (`server.rs:608-610`)
  - `id_token_add_organizations=true`
  - `codex_cli_simplified_flow=true`
  - `originator=<originator value>` (default `codex_cli_rs`, see F)
  - `allowed_workspace_id=<comma-joined ids>` — only when the CLI was started with a forced workspace restriction (`server.rs:601-603`)
  - No `resource` parameter is sent on this flow (`resource: None` in `server.rs:611`).
- **PKCE generation** (`codex-rs/login/src/oauth/pkce.rs:14-29`): `code_verifier` = 64 random bytes, base64url-nopad; `code_challenge` = base64url-nopad(SHA256(verifier)); method is always `S256`.
- **State**: 32 random bytes, base64url-nopad (`codex-rs/login/src/oauth/authorization.rs:44-48`), unless the caller forces a fixed state for testing.
- **Local callback server**: binds `127.0.0.1:1455` (`DEFAULT_PORT`, `server.rs:78`) at path `/auth/callback`. If the port is busy: sends a `GET /cancel` HTTP request to `127.0.0.1:1455` on the assumption a stale previous login server is listening (`send_cancel_request`, `server.rs:620-635`), retries binding up to 10 times with a 200 ms delay (`server.rs:637-695`); if still unavailable after 10 attempts **and** the default port was requested, it falls back once to port **1457** (`FALLBACK_PORT`, `server.rs:80`, comment: "Keep in sync with the Codex CLI Hydra redirect URI allow-list") and repeats the same retry loop; if the fallback port is also unavailable it returns `AddrInUse`.
- **Token endpoint**: `{issuer}/oauth/token` — `codex-rs/login/src/server.rs:719`.
- **Exchange request** (`codex-rs/login/src/oauth/client.rs:58-74`, `TokenEncoding::Form`, `Content-Type: application/x-www-form-urlencoded`):
  `grant_type=authorization_code&client_id=...&code=...&redirect_uri=...&code_verifier=...` (no `resource` on this flow).
- **Token response fields** (`ExchangedTokens`, `codex-rs/login/src/server.rs:698-703`): `id_token`, `access_token`, `refresh_token` (all `String`, required). The struct does **not** declare an `expires_in` field — serde ignores unknown fields by default, so if the server sends one it is silently dropped; Codex derives expiry from the access token's own `exp` JWT claim instead (see C).
- After exchange, Codex calls `obtain_api_key` (token-exchange grant, see E) and persists tokens via `persist_tokens_async` (`server.rs:447-465, 832-884`).

## B. Device-code sign-in

Entered via `codex login --device-auth` (or automatically as a headless fallback — see `codex-rs/cli/src/login.rs:370-445`). All logic in `codex-rs/login/src/device_code_auth.rs`.

- **Step 1 — request a user code**: `POST {issuer}/api/accounts/deviceauth/usercode`, JSON body `{"client_id": "<client id>"}` (`request_user_code`, `device_code_auth.rs:63-97`).
  - Response: `{"device_auth_id": "...", "user_code"|"usercode": "...", "interval": "<seconds, as string or number>"}` (`UserCodeResp`, `device_code_auth.rs:27-34`).
  - A `404` response is mapped to: *"device code login is not enabled for this Codex server. Use the browser login or verify the server URL."* (`device_code_auth.rs:83-88`) — this is a **server-side feature flag**, not a client-visible ChatGPT account setting.
- **Verification URL shown to the user**: `{issuer}/codex/device` (`request_device_code`, `device_code_auth.rs:165-180`).
- **User-facing prompt** (`device_code_prompt`, `device_code_auth.rs:149-158`), quoted verbatim:
  > ```
  > Welcome to Codex [v<version>]
  > OpenAI's command-line coding agent
  >
  > Follow these steps to sign in with ChatGPT using device code authorization:
  >
  > 1. Open this link in your browser and sign in to your account
  >    <verification_url>
  >
  > 2. Enter this one-time code (expires in 15 minutes)
  >    <user_code>
  >
  > Continue only if you started this login in Codex. If a website or another person gave you this code, cancel.
  > ```
- **Step 2 — poll for the code**: `POST {issuer}/api/accounts/deviceauth/token`, JSON body `{"device_auth_id": "...", "user_code": "..."}` (`poll_for_token`, `device_code_auth.rs:100-147`).
  - `200` → `{"authorization_code": "...", "code_challenge": "...", "code_verifier": "..."}` (`CodeSuccessResp`, `device_code_auth.rs:55-60`). **The PKCE verifier/challenge pair is generated server-side** and handed back to the client here — unlike the browser flow, the device-code client never generates its own PKCE values.
  - `403` or `404` → treated as "still pending"; sleeps for `interval` seconds (capped so the total wait never exceeds the timeout) and retries.
  - Hard timeout: **15 minutes** (`max_wait = Duration::from_secs(15 * 60)`, `device_code_auth.rs:108`) → `"device auth timed out after 15 minutes"`.
  - Any other status → `"device auth failed with status {status}"`.
- **Step 3 — final token exchange**: reuses the browser flow's `exchange_code_for_tokens` with `redirect_uri = {issuer}/deviceauth/callback` and the server-issued PKCE pair (`complete_device_code_login`, `device_code_auth.rs:182-236`). No `obtain_api_key` call is made on this path (`api_key` is passed as `None` to `persist_tokens_async`, `device_code_auth.rs:228`) — only the browser flow attempts the id_token→API-key exchange.
- **Prerequisite**: I found **no documented ChatGPT-account-side toggle** the user must enable. The only gate observed in source is server-side: a `404` on `/deviceauth/usercode` means "not enabled for this server," which the CLI's fallback path (`run_login_with_device_code_fallback_to_browser`, `codex-rs/cli/src/login.rs:374-445`) catches by `io::ErrorKind::NotFound` and silently drops back to the browser flow, printing *"Device code login is not enabled; falling back to browser login."* (`login.rs:419`). Separately, there is a **local, operator-side** restriction unrelated to device-code specifically: `ForcedLoginMethod::{Chatgpt, Api}` (`codex-rs/protocol/src/config_types.rs:559-562`) can disable ChatGPT login entirely via config, in which case both device-code and browser paths print *"ChatGPT login is disabled. Use API key login instead."* (`CHATGPT_LOGIN_DISABLED_MESSAGE`, `codex-rs/cli/src/login.rs:42-43`) before even starting. This is a config/admin-policy gate, not a per-account ChatGPT UI setting.

## C. Refresh

- **Endpoint**: `https://auth.openai.com/oauth/token` — same URL as the authorization-code exchange (`REFRESH_TOKEN_URL`, `codex-rs/login/src/auth/manager.rs:212`), overridable via `CODEX_REFRESH_TOKEN_URL_OVERRIDE` (`manager.rs:214, 1727-1730`).
- **Client ID**: `oauth_client_id()` → `app_EMoamEEZ73f0CkXaXp7hrann` (`CLIENT_ID`, `manager.rs:1718`), same constant as login, overridable via `CODEX_APP_SERVER_LOGIN_CLIENT_ID` (`manager.rs:216, 1720-1725`).
- **Request body/encoding**: unlike the authorization-code exchange (form-encoded), the refresh call uses **JSON** (`TokenEncoding::Json`, `manager.rs:1633-1642`; encoding dispatch in `codex-rs/login/src/oauth/client.rs:100-111`): `{"grant_type":"refresh_token","client_id":"...","refresh_token":"..."}` (no `resource` field on this flow, `manager.rs:1643-1648`).
- **When Codex refreshes**:
  - *Proactively*, inside `AuthManager::auth()` (`manager.rs:2391-2405`), called whenever code asks "give me the current auth," via `should_refresh_proactively` (`manager.rs:3001-3023`):
    1. If the access-token JWT has a parseable `exp` claim: refresh if `exp <= now + 5 minutes` (`CHATGPT_ACCESS_TOKEN_REFRESH_WINDOW_MINUTES = 5`, `manager.rs:204, 3011-3017`).
    2. Else (no parseable `exp`): fall back to refreshing if `last_refresh` is more than **8 days** old (`TOKEN_REFRESH_INTERVAL = 8`, `manager.rs:203, 3018-3022`).
  - *Reactively on 401*: `UnauthorizedRecovery` (`manager.rs:1863-2032`) runs a small state machine — first `Reload` (re-read `auth.json` from disk, in case a sibling process already refreshed it), then `RefreshToken` (actually call the refresh endpoint via `refresh_token_from_authority`), then `Done`. Each step is attempted at most once per recovery.
- **Refresh-token rotation**: the response struct `RefreshResponse { id_token: Option<String>, access_token: Option<String>, refresh_token: Option<String> }` (`manager.rs:1710-1715`) makes the new `refresh_token` **optional**. `persist_tokens` (`manager.rs:1600-1623`) only overwrites the stored `refresh_token` `if let Some(refresh_token) = refresh_token` — i.e., Codex adopts a new refresh token whenever the server sends one, but keeps the old one if the server omits it. The existence of a dedicated `RefreshTokenFailedReason::Exhausted` ("reused") error class is direct evidence that **the server does enforce rotation and single-use** of refresh tokens.
- **Reusing an old/expired/revoked refresh token fails**, classified by `classify_refresh_token_failure` (`manager.rs:1680-1708`) from the token endpoint's JSON error `code` field (case-insensitive):
  | Server error code | Reason | User-facing message |
  |---|---|---|
  | `refresh_token_expired` | `Expired` | "Your access token could not be refreshed because your refresh token has expired. Please log out and sign in again." |
  | `refresh_token_reused` | `Exhausted` | "Your access token could not be refreshed because your refresh token was already used. Please log out and sign in again." |
  | `refresh_token_invalidated` | `Revoked` | "Your access token could not be refreshed because your refresh token was revoked. Please log out and sign in again." |
  | anything else | `Other` | "Your access token could not be refreshed. Please log out and sign in again." |

  (constants at `manager.rs:206-211`). A request is classified **permanent** (non-retryable) when the HTTP status is `401`, **or** the reason above is not `Other`, **or** the status is `400` with error code `invalid_grant` (case-insensitive) — all other failures are `Transient` and may be retried (`manager.rs:1659-1671`). Separately, if a cached auth's account no longer matches the AuthManager's expected account, recovery fails permanently with: "Your access token could not be refreshed because you have since logged out or signed in to another account. Please sign in again." (`REFRESH_TOKEN_ACCOUNT_MISMATCH_MESSAGE`, `manager.rs:211, 2006-2009`).

## D. Identity

- **Account ID**: `CodexAuth::get_account_id()` (`codex-rs/login/src/auth/manager.rs:613-626`) reads `TokenData.account_id`, which was captured **once, at login time**, from the **id_token's** `chatgpt_account_id` claim (`persist_tokens_async`, `codex-rs/login/src/server.rs:856-861`). The claim lives under the `https://api.openai.com/auth` namespace (`AuthClaims.chatgpt_account_id`, `codex-rs/login/src/token_data.rs:77-98`).
- **Plan type**: `IdTokenInfo.chatgpt_plan_type` (`token_data.rs:34`), parsed from the same `https://api.openai.com/auth` claim object's `chatgpt_plan_type` field, typed as `PlanType::{Known(_), Unknown(String)}`; comment says observed values include `"free", "plus", "pro", "business", "enterprise", "edu"` (`token_data.rs:31-33`) but "values may vary by backend."
- **Claim path, precisely** (`parse_chatgpt_jwt_claims`, `token_data.rs:174-198`): decode the JWT payload as
  ```
  { "email": ..., "https://api.openai.com/profile": {"email": ...}, "https://api.openai.com/auth": {
      "chatgpt_plan_type": ..., "chatgpt_user_id": ..., "user_id": ..., "chatgpt_account_id": ..., "chatgpt_account_is_fedramp": ... } }
  ```
  `email` falls back to the profile namespace if the top-level claim is absent; `chatgpt_user_id` falls back to `user_id`.
- **Signature verification**: for the **normal ChatGPT id_token/access_token**, Codex does **not** verify the JWT signature at all. `decode_jwt_payload` (`token_data.rs:129-140`) splits the JWT on `.`, requires all three segments to be non-empty, base64url-decodes only the **payload** segment, and parses it as JSON — the signature segment is never cryptographically checked. This function backs both `parse_chatgpt_jwt_claims` (identity) and `parse_jwt_expiration` (refresh timing, `token_data.rs:142-147`).
- Contrast: Codex **does** own real JWT-signature-verification machinery (the `jsonwebtoken` crate, RS256, against a JWKS fetched from `{chatgpt_base_url}/wham/agent-identities/jwks`) — but only for the separate **`AgentIdentity`** auth mode (`codex-rs/login/src/auth/agent_identity.rs:259-275`), not for the human ChatGPT sign-in path described in A/B. When an agent-identity JWT is merely reloaded from local storage (not freshly bootstrapped), it is decoded with `jwks: None`, i.e. unverified (`codex-rs/login/src/auth/storage.rs:139-144`).
- **account_user_id** (a narrower, workspace-scoped identity) is derived from the **access_token** (not the id_token) via `parse_chatgpt_account_user_id` (`token_data.rs:152-172`), and is only trusted if the access token's own `chatgpt_account_id` claim matches the already-stored `account_id` — i.e. no fallback to a mismatched workspace.

## E. Storage

- **Location**: `$CODEX_HOME/auth.json` (`get_auth_file`, `codex-rs/login/src/auth/storage.rs:162-164`). `CODEX_HOME` defaults to `~/.codex` when unset, and if set must already exist and be a directory (`codex-rs/utils/home-dir/src/lib.rs:5-18`).
- **File permissions**: `0o600` on Unix, opened with `truncate().write(true).create(true)` (`FileAuthStorage::save`, `storage.rs:214-231`).
- **`auth.json` schema** (`AuthDotJson`, `storage.rs:48-73`), fields (all optional except noted):
  - `auth_mode` (`AuthMode` enum)
  - `"OPENAI_API_KEY"` (serialized under that literal renamed key) → `openai_api_key: Option<String>`
  - `tokens: Option<TokenData>` — `{ id_token (serialized back out as the raw JWT string, deserialized into parsed claims — see D), access_token, refresh_token, account_id }` (`codex-rs/login/src/token_data.rs:10-25`)
  - `last_refresh: Option<DateTime<Utc>>`
  - `agent_identity: Option<AgentIdentityStorage>` (untagged enum: a raw JWT string, or a structured record)
  - `personal_access_token: Option<String>`
  - `bedrock_api_key`, `bedrock_access_keys` (Bedrock-specific, not relevant here)
- **Which token is actually used as the bearer**: `CodexAuth::get_token()` (`codex-rs/login/src/auth/manager.rs:593-610`) returns `tokens.access_token` for `Chatgpt`/`ChatgptAuthTokens` auth — **not** `openai_api_key`.
- **Alternative store (keyring)**: selected by the config key **`cli_auth_credentials_store`** (TOML, `codex-rs/config/src/config_toml.rs:290`), type `AuthCredentialsStoreMode` (`codex-rs/config/src/types.rs:134-146`, serialized lowercase): `file` (default — "Persist credentials in CODEX_HOME/auth.json"), `keyring` ("fail if unavailable"), `auto` ("keyring when available, else file"), `ephemeral` ("in memory only for the current process"). A further `AuthKeyringBackendKind` chooses between a `Direct` OS-keyring entry (service name `"Codex Auth"`, key `cli|<16-hex sha256 of canonicalized CODEX_HOME>"`, `storage.rs:238-346`) and a `Secrets`-manager-backed store (`storage.rs:348-450`). Saving to either keyring mode deletes the on-disk fallback file (`storage.rs:325-330, 427-432`).
- **id_token → API-key token-exchange**: **yes, still present** in this version, but **only on the interactive browser flow**, and **best-effort** (its own failure is swallowed with `.ok()` and does not fail the login). After the authorization-code exchange, `obtain_api_key` (`codex-rs/login/src/server.rs:1022-1057`) does:
  ```
  POST {issuer}/oauth/token
  Content-Type: application/x-www-form-urlencoded
  grant_type=urn:ietf:params:oauth:grant-type:token-exchange
  &client_id=<client id>
  &requested_token=openai-api-key
  &subject_token=<id_token>
  &subject_token_type=urn:ietf:params:oauth:token-type:id_token
  ```
  Response: `{"access_token": "<api key>"}`, stored as `OPENAI_API_KEY` in `auth.json` (`server.rs:442-446, 862-865`). Crucially, **this exchanged value is not what's sent as the bearer on model requests** under ChatGPT auth — see "which token is used" above and section F. The device-code flow (B) skips this step entirely (`device_code_auth.rs:228`, `api_key` hardcoded to `None`).

## F. Model requests with ChatGPT auth

- **Base URL selection** (`ModelProviderInfo::to_api_provider`, `codex-rs/model-provider-info/src/lib.rs:427-445`): if the resolved `auth_mode` is one of `Chatgpt | ChatgptAuthTokens | Headers | AgentIdentity | PersonalAccessToken` **and** the provider config has no explicit `base_url` override, the default base URL is `CHATGPT_CODEX_BASE_URL = "https://chatgpt.com/backend-api/codex"` (`codex-rs/model-provider-info/src/lib.rs:80`); otherwise (API-key auth) it's `"https://api.openai.com/v1"`.
- **Path**: the Responses HTTP client always posts to path `/responses` (`codex-rs/codex-api/src/endpoint/responses.rs:67`), so the full ChatGPT-auth endpoint is **`https://chatgpt.com/backend-api/codex/responses`**.
- **Headers** actually attached to a normal (non-websocket) streaming request:
  - `Authorization: Bearer <access_token>` and, if an account id is known, `ChatGPT-Account-ID: <account_id>` and (FedRAMP workspaces only) `X-OpenAI-Fedramp: true` — all three added by `BearerAuthProvider::add_auth_headers` (`codex-rs/model-provider/src/bearer_auth_provider.rs:31-46`), selected for Chatgpt/ChatgptAuthTokens/ApiKey/PersonalAccessToken auth by `auth_provider_from_auth` (`codex-rs/model-provider/src/auth.rs:307-325`).
  - `originator: codex_cli_rs` (`DEFAULT_ORIGINATOR`, `codex-rs/login/src/auth/default_client.rs:42`) — set once as a default header on the shared HTTP client (`default_client.rs:458`); a per-call override (`add_originator_header`, `default_client.rs:125-139`) only fires when a sub-agent/internal originator differs from the process default.
  - `User-Agent: <originator>/<version> (<os-type> <os-version>; <arch>) <terminal-detection user agent>[ (<suffix>)]` (`get_codex_user_agent`, `default_client.rs:152-179`).
  - `session-id: <uuid>` and `thread-id: <uuid>` (literal header names, **not** `session_id`/`conversation_id`) — `codex-rs/codex-api/src/requests/headers.rs:5-13`, built from `ResponsesOptions.session_id`/`.thread_id` which `ModelClient` fills from its own session/thread state (`codex-rs/core/src/client.rs:1373-1374`).
  - `x-codex-beta-features: <value>` — only if configured (`build_responses_headers`, `codex-rs/core/src/client.rs:2336-2353`).
  - `x-codex-turn-state: <value>` — only if a turn state has been recorded (same function).
  - A handful of conditional, feature-gated headers: `X-OAI-Attestation` (compliance/attestation; `generate_attestation_header_for`, inserted at `codex-rs/core/src/client.rs:1386-1388` inside `build_responses_options`), `x-codex-routing-hint` (workspace routing for Business/Enterprise ChatGPT workspaces; computed by `build_routing_hint_header` and inserted into `options.extra_headers` at `codex-rs/core/src/client.rs:1737-1746`).
  - **`OpenAI-Beta` is NOT sent on this plain HTTP/SSE path** in this version. The only two non-test call sites for `OPENAI_BETA_HEADER` in the whole repo (`codex-rs/core/src/client.rs:1328-1330` and `codex-rs/cli/src/doctor.rs:2360-2361`) both set it to `RESPONSES_WEBSOCKETS_V2_BETA_HEADER_VALUE = "responses_websockets=2026-02-06"` (`client.rs:175`), used only when **opening the optional WebSocket transport** (`build_websocket_headers`, `client.rs:1303-1338`), not the standard HTTP streaming path. (pi-mono disagrees here — see "Disagreements.")
  - `version`/`X-OpenAI-*` version headers: not found as a distinct header; the client identifies its version only via the `User-Agent` string and the `?client_version=` query parameter on the **models** endpoint (see H), not on `/responses`.
- **Request body** (`ResponsesApiRequest`, `codex-rs/codex-api/src/common.rs:279-304`; built in `codex-rs/core/src/client.rs:891-1013`), field by field:
  - `model`, `input` (array), `tools`, `tool_choice` — **always the literal string `"auto"`** (`client.rs:1000`), `parallel_tool_calls` (bool), `reasoning` (**always present**, built by `build_reasoning`, `client.rs:869-888`: `effort` from config or model default, `summary` only if the model supports the summary parameter, `context: AllTurns` only when "Responses Lite" is disabled), `store` — **always hardcoded `false`** (`client.rs:1003`), `stream` — **always hardcoded `true`** (`client.rs:1004`; there is no non-streaming request path), `stream_options`, `include` — **always exactly `["reasoning.encrypted_content"]`** (`client.rs:961, 1006`), `prompt_cache_key` — always set, derived from an explicit override, or `"{internal-source}:{parent_thread_id}"`, or else the Responses session-id (`prompt_cache_key`, `client.rs:581-593`), `text` (verbosity/output-schema controls), `client_metadata`, `access_programs` (cyber-access-program gating, usually `None`).
  - **`instructions` does not exist as a top-level field at all** in this struct. Instead, when `prompt.base_instructions.text` is non-empty, Codex **prepends it to the `input` array as the first item**, as a synthetic message with `role: "developer"` (`BaseInstructionsFragment`, `codex-rs/core/src/context/base_instructions.rs:26-46`, `role()` returns `"developer"` at line 32), built in `client.rs:931-940`. Whether the ChatGPT backend still validates the *content* of this developer-role message against an official Codex system prompt is **UNVERIFIED** — that would be server-side logic invisible from the client source; what's verifiable is only that the client sends the prompt as the first `input` item, not as a dedicated `instructions` string.
  - **Not present at all, on any auth mode, in this struct**: `max_output_tokens`, `temperature`, `top_p`. `previous_response_id` exists only on the separate WebSocket-specific `ResponseCreateWsRequest` struct (`codex-api/src/common.rs:330-370`-ish) and the one `From<&ResponsesApiRequest>` conversion I read sets it unconditionally to `None` (`codex-api/src/common.rs:306-328`) — I did not find a code path that populates a real value for the plain HTTP flow (UNVERIFIED whether the WebSocket-native construction path sets a real one elsewhere; out of scope for a plain-HTTP design and not fully traced).
- **pi-mono's independent implementation** (`packages/ai/src/api/openai-codex-responses.ts:552-601`) builds a very similar but not identical body: `model, store: false, stream: true, instructions: <system text> || "You are a helpful assistant.", input, text: {verbosity}, include: ["reasoning.encrypted_content"], prompt_cache_key, tool_choice: "auto", parallel_tool_calls: true`, conditionally `temperature`, `service_tier`, `tools`, `reasoning`. Their own comment (`openai-codex-responses.ts:1520-1521`) is a strong, concrete confirmation of a **server-side constraint**: *"ChatGPT Codex Responses rejects `store: true` (\"Store must be set to false\"). WebSocket continuation still works via connection-scoped previous_response_id state."* — i.e. the backend actively rejects `store: true` with that literal error message, which is why both clients hardcode `store: false`.

## G. Responses from that endpoint

- **SSE event shape**: the event-type taxonomy Codex's SSE parser recognizes (`codex-rs/codex-api/src/sse/responses.rs:347-489`) is the **same** as the public OpenAI Responses API: `response.created`, `response.in_progress`, `response.output_item.added`/`.done`, `response.output_text.delta`/`.done`, `response.content_part.added`/`.done`, `response.function_call_arguments.delta`/`.done`, `response.custom_tool_call_input.delta`/`.done`, `response.reasoning_summary_text.delta`/`.done`, `response.reasoning_summary_part.added`/`.done`, `response.reasoning_text.delta`, `response.completed`, `response.incomplete`, `response.failed`. One addition beyond the stock public API: **`response.metadata`**, a Codex/ChatGPT-specific event carrying extra fields such as a nested `"type": "safety_buffering"` payload (`sse/responses.rs:211-253`) — this is the one documented shape difference I found.
- **Rate-limit headers** Codex reads, via `parse_rate_limit_for_limit` (`codex-rs/codex-api/src/rate_limits.rs:57-102`): for a limit id (default `"codex"`, normalized to kebab-case) it reads `x-{limit}-primary-used-percent`, `x-{limit}-primary-window-minutes`, `x-{limit}-primary-reset-at`, and the `-secondary-` equivalents, plus `x-{limit}-limit-name`. It also reads credit headers `x-codex-credits-has-credits`, `x-codex-credits-unlimited`, `x-codex-credits-balance` (`parse_credits_snapshot`, `rate_limits.rs:219-231`), a global promo message `x-codex-promo-message` (`rate_limits.rs:181-186`), and `x-codex-rate-limit-reached-type` (`rate_limits.rs:188-193`). `parse_all_rate_limits` (`rate_limits.rs:28-51`) discovers *every* metered-limit family present by scanning header names for the `-primary-used-percent` suffix — so OpenAI can introduce new named limits (e.g. the test fixture's `x-codex-bengalfox-*`) without a client code change.
- **Usage-limit error shape**, from `map_api_error_details` (`codex-rs/codex-api/src/api_bridge.rs:181-235`): HTTP **`429 Too Many Requests`**, JSON body `{"error": {"code": ..., "type": ..., "plan_type": ..., "resets_at": <unix seconds>, "limit_window_minutes": <number>}}` (`UsageErrorResponse`/`UsageErrorBody`, `api_bridge.rs:340-352`).
  - `error.type == "usage_limit_reached"` → `CodexErr::UsageLimitReached { plan_type, resets_at (converted to a `DateTime<Utc>`), limit_window_minutes, rate_limits (parsed from the `x-codex-active-limit`-named family, `ACTIVE_LIMIT_HEADER = "x-codex-active-limit"`, `api_bridge.rs:189, 275`), promo_message, rate_limit_reached_type }`.
  - `error.type == "usage_not_included"` → `CodexErr::UsageNotIncluded`.
  - `error.type == "insufficient_quota"` **or** `error.code` in `{insufficient_quota, credit_balance_exhausted, organization_spend_limit_exceeded, project_spend_limit_exceeded, organization_usage_limit_exceeded}` → `CodexErr::QuotaExceeded`.
  - Separately, a `503 Service Unavailable` body with `error.code == "server_is_overloaded"` → `CodexErr::ServerOverloaded`; `error.code == "slow_down"` → treated as a rate-limit-exceeded condition (`api_bridge.rs:101-118`).
- **How Codex reports it**: these map into the `CodexErr`/`CodexErrorDetails` enum that the TUI and other front-ends render; I did not trace the exact end-user string the TUI prints for `UsageLimitReached` (out of scope for the question of wire behavior, and would require reading the whole `tui` crate) — marked UNVERIFIED below.
- **pi-mono cross-check** (`openai-codex-responses.ts:1597-1622`) independently confirms the same JSON error shape — it reads `error.code`, `error.type`, `error.plan_type`, `error.resets_at` from a `429` body and additionally treats `error.code`/`type` matching `/usage_limit_reached|usage_not_included|rate_limit_exceeded/i` as a usage-limit condition, surfacing a friendly string: `"You have hit your ChatGPT usage limit (<plan> plan). Try again in ~<n> min."`

## H. Models

- **Not** a hard-coded preset list for ChatGPT auth. Codex fetches a live catalog from **`GET {CHATGPT_CODEX_BASE_URL}/models?client_version=<version>`**, i.e. `https://chatgpt.com/backend-api/codex/models?client_version=<version>` (`codex-rs/model-provider/src/models_endpoint.rs:119, 548`; the comment at line 118 reads: *"Codex metadata is served by the Codex backend, not the public /v1/models API"*). This same Codex-backend `/models` endpoint is used even for **API-key auth**, as long as the provider has no explicit `base_url`/`model_catalog_url` override (`models_endpoint.rs:113-120`) — only Amazon Bedrock has a genuine hard-coded `default_model_catalog()` fallback (`codex-rs/model-provider/src/amazon_bedrock/mod.rs:249`), used when Bedrock's own catalog call fails.
- The fetched catalog is cached on disk and refreshed under a `RefreshStrategy` (`OpenAiModelsManager::raw_model_catalog`, `models_endpoint.rs` tests at lines 520-553) rather than re-fetched on every call.
- I could not enumerate the **current** set of model IDs offered under ChatGPT auth from source — that list is server-driven and not present as a static table in either codebase I read. pi-mono's parallel static catalog file (`packages/ai/src/providers/data/openai-codex.json`, referenced from `openai-codex.models.ts:4`) was **not present in the shallow clone** (it's generated by `packages/ai/scripts/generate-models.ts`, almost certainly fetched/gitignored rather than checked in), so I have no list to report even as a stale secondary data point. Marked UNVERIFIED.

## I. Policy

I found **no single, formal OpenAI legal/policy document** that directly states whether third parties may reimplement Codex's own OAuth client (same `client_id`, same `chatgpt.com/backend-api/codex` endpoint) the way this research describes it. What I did find, in descending order of authority:

1. **A direct statement from OpenAI's Codex lead**, Thibault Sottiaux ("Tibo", `@thsottiaux`), replying to a developer on X about building a third-party client against Sign-in-with-ChatGPT for Codex (found via WebSearch result snippet of the live post; WebFetch of the post itself returned HTTP 402 for this session, so I could not independently re-render the page — the quote below is as captured by the search index, not re-verified from the raw page):
   > "Sorry, but this is not an approved use of Sign in With ChatGPT. We support pure OSS clients and others we have partnerships with, but you need to reach out and talk to us for that. Please see DM."
   — [x.com/thsottiaux/status/2097131394199896166](https://x.com/thsottiaux/status/2097131394199896166)

   This squarely says: unaffiliated third-party tools using this OAuth mechanism for Codex are **not approved** unless the tool is "pure OSS" or the author has an explicit partnership with OpenAI.

2. **GitHub Discussion** [openai/codex#8338 — "Does forking/modifying Codex CLI affect ToS when using 'Sign in with ChatGPT'?"](https://github.com/openai/codex/discussions/8338): an OpenAI maintainer, `etraut-openai` (Dec 19, 2025), answered only the licensing half of the question:
   > "The codex CLI sources are licensed under a permissive Apache license, and you're welcome to fork the repo and make modifications to suit your own needs."
   They pointed to the general [Terms of Use](https://openai.com/policies/row-terms-of-use/) and suggested looking at the "skills" feature as an alternative, but — per multiple follow-up questions from other developers through mid-2026 — **never explicitly confirmed or denied** that a fork may keep using Sign-in-with-ChatGPT OAuth once distributed to other users. That half of the question remained unanswered in the thread as of my read.

3. **A separate, officially-sanctioned program** also called "Sign in with ChatGPT" exists for third-party *apps* (not the Codex-CLI-shaped OAuth client specifically) — this is a different, partner-gated integration surface, launched in phases through 2026 (rollout beginning around July 29, 2026 per release notes, expanded at DevDay, Sept 29, 2026) with named launch partners (OpenCode, Devin, Amp, Warp, Notion, Vercel, and others). Per secondary reporting of OpenAI's own help-center content (I could not fetch `help.openai.com` directly — it returned HTTP 403 to this session both times I tried):
   - Eligible plans: sources disagree on the exact list — one secondary source says "ChatGPT Free, Plus, and Pro," another (a search-engine-quoted snippet of the help article "Using your ChatGPT plan in other apps and sites") says "Go, Plus, or Pro"; Enterprise, Education, and Team are excluded "for now" in both tellings.
   - Data shared with the partner app: name, email address, profile picture.
   - Usage controls: the ChatGPT user sets a **weekly cap per connected app** (as a percentage of their total weekly plan usage) in ChatGPT Settings → Usage/Connected apps; usage simply **stops** once the cap is hit rather than falling back to the partner's own billing.
   - This program requires the third party to be a listed/approved partner — it is not a documented self-service OAuth client-registration flow a new, unaffiliated tool can join on its own.
   - Sources: [help.openai.com/en/articles/20001542](https://help.openai.com/en/articles/20001542-using-your-chatgpt-plan-in-other-apps-and-sites) (fetch blocked, 403; characterized via search snippet only), [help.openai.com/en/articles/11369540](https://help.openai.com/en/articles/11369540-using-codex-with-your-chatgpt-plan) (fetch blocked, 403), [thenewstack.io/sign-in-with-chatgpt](https://thenewstack.io/sign-in-with-chatgpt/) (fetch returned only a newsletter-signup shell, no article body), [medianama.com, June 2025 launch piece](https://www.medianama.com/2025/06/223-openai-sign-in-with-chatgpt-third-party-apps/).

**Net reading**: the specific mechanism this research documents (A–H) — hitting Codex's own internal OAuth `client_id` and `chatgpt.com/backend-api/codex` endpoint directly, the way the Codex CLI itself does — is, per OpenAI's own Codex lead, explicitly **not an approved use** for a third party unless that party is a pure-open-source client or has a direct partnership with OpenAI. The separate, broader "Sign in with ChatGPT" partner program is a different (and actually-sanctioned) integration surface, but it is partner-gated, not self-service, and I found no evidence it is the same OAuth client/endpoint described in A–H.

---

## Disagreements between sources

- **`OpenAI-Beta` header on the plain HTTP/SSE path.** Official Codex Rust client (0.162.1): the only two non-test call sites for this header in the whole repo (`codex-rs/core/src/client.rs:1328-1330`, `codex-rs/cli/src/doctor.rs:2360-2361`) set it to `"responses_websockets=2026-02-06"` and only on the **WebSocket** handshake, never on the plain HTTP streaming request. pi-mono's HTTP SSE path (`buildSSEHeaders`, `openai-codex-responses.ts:1671-1673`) **does** set `OpenAI-Beta: responses=experimental` on every plain HTTP request (a different value than the websocket one, which pi-mono also implements separately with the identical `"responses_websockets=2026-02-06"` value at `openai-codex-responses.ts:866, 1696`). I cannot determine from source alone whether the backend still requires/honors `responses=experimental` on HTTP, or whether pi-mono is carrying a vestigial header the current backend ignores.
- **OAuth `scope`.** Official Codex 0.162.1 requests `"openid profile email offline_access api.connectors.read api.connectors.invoke"` (`codex-rs/login/src/server.rs:608-610`). pi-mono requests only `"openid profile email offline_access"` (`packages/ai/src/auth/oauth/openai-codex.ts:34`) — missing the two connector scopes entirely. Both are evidently able to obtain working tokens, so these connector scopes look additive/optional rather than required.
- **`instructions` field.** pi-mono sends a conventional top-level `instructions: string` field on every request, defaulting to the literal string `"You are a helpful assistant."` when no system message is present (`openai-codex-responses.ts:558`). The official Rust client has **no such field** in its request struct at all in this version — it folds the base/system prompt into `input[0]` as a `role: "developer"` message instead (`codex-rs/core/src/context/base_instructions.rs:26-46`; `codex-rs/core/src/client.rs:931-940`). This looks like a genuine behavioral difference between the two clients' wire format, not just a documentation gap — whether the ChatGPT backend treats these two shapes equivalently is unverified from either client's source.
- **`temperature`.** pi-mono conditionally sends `temperature` in the body (`openai-codex-responses.ts:567-569`) if the caller specified one. The official Rust `ResponsesApiRequest` struct has no `temperature` field at all — it is never sent, regardless of configuration.
- **Port-busy recovery on browser login.** Official Codex retries binding port 1455 for ~2 seconds, attempts to cancel a possibly-stale prior login server, and then falls back to a fixed alternate port 1457 (`codex-rs/login/src/server.rs:637-695`). pi-mono makes no such attempt: on a bind failure it just gives up on the local callback server entirely and asks the user to paste the authorization code or redirect URL manually (`openai-codex.ts:364-385`, comment: "Port 1455 is shared with the Codex CLI; when it is taken, fall back to the pasted redirect URL").
- **Eligible ChatGPT plans for the *partner* Sign-in-with-ChatGPT program** (not the Codex-specific OAuth in A–H): secondary sources disagree on "Free, Plus, Pro" vs. "Go, Plus, Pro." I could not resolve this from the primary help.openai.com article because it returned HTTP 403 to this session.

## UNVERIFIED

- Whether the ChatGPT backend validates the content of the `developer`-role base-instructions message (or, in pi-mono's shape, the `instructions` field) against a known-good/official Codex system prompt. This would be server-side logic invisible from either client's source; I found no evidence either way.
- Whether `previous_response_id` is ever populated with a real value on any Codex code path — the one conversion I read (`codex-rs/codex-api/src/common.rs:306-328`) hardcodes it to `None`; there may be a separate native-WebSocket construction path that sets a real value for conversation continuation, which I did not fully trace.
- The exact end-user-visible string the Codex **TUI** prints when `CodexErr::UsageLimitReached`/`QuotaExceeded` is returned (I traced the error classification in `codex-api`/`core` but did not read the `tui` crate's rendering of it).
- The current, live list of model IDs actually offered under ChatGPT auth — this is server-driven (see H) and not present as a static table in either client's source at this commit.
- Exact wording/scope of OpenAI's `help.openai.com` articles on "Using your ChatGPT plan in other apps and sites" and "Using Codex with your ChatGPT plan" — both returned HTTP 403 to WebFetch in this session; everything attributed to them here is via search-engine snippet only, not a direct read of the page.
- Whether the X post by `@thsottiaux` (quoted in section I) is accurately rendered by the search snippet — WebFetch of the live post itself returned HTTP 402 for this session, so the quote could not be independently re-verified against the raw page, only against the search index's own quotation of it.
- Whether `codex login --device-auth` has any additional, undocumented workspace-admin-policy prerequisite beyond the `ForcedLoginMethod` config switch and the server-side 404 feature-gate described in B — I searched the shipped docs/CLI help text in this checkout and found no mention of a ChatGPT-side toggle, but that is an absence-of-evidence finding, not a confirmed absence.
