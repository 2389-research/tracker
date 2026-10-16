# ChatGPT Subscription for the openai Provider — Implementation Plan

## Now
- Step: PR for #700 + #701 from `fix/700-702-chatgpt-followups` (e95336bc #701, 3b3c576d #700). Ruling: #702 ships as its own PR — correct per-response attribution adds a per-provider split to `SessionStats` and the turn snapshot, so it gets its own review; the split appears only when a session used more than one label, so single-provider stats and the golden traces don't change.
- Next: #702 on its own branch from main; file the parallel-branch `unknown` provider bucket (mergeSessionStats drops Provider) as a separate issue once a test confirms it.
- Open: none.
- Approved: "Yes, build it (Recommended)" (2026-10-15) — build under the open-source-client terms (spec D3)
- Approved: "Looks right, write it" (2026-10-15) — design sections 1–4, write the spec
- Approved: "approved" (2026-10-15) — the written spec, docs/superpowers/specs/2026-10-15-chatgpt-subscription-design.md
- Approved: "yes. i do" (2026-10-15) — this plan, executed subagent-driven
- Approved: "1 (pull before)" (2026-10-16) — merge back to main locally, pulling main first (finishing-a-development-branch option 1)
- Approved: "Push now (Recommended)" (2026-10-16) — push main to origin and watch CI
- Approved: "Push it (Recommended)" (2026-10-16) — push the final docs commit (plan Now + gotchas)
- Approved: "perfect. tag a release" (2026-10-16) — cut and tag the next release, v0.80.0. Not written down when given; added after compaction 1 from the session transcript (user message, 18:30Z)
- Approved: "Land #699 first (Recommended)" (2026-10-16) — land `--autopilot-model` (#699) before tagging; from the transcript (18:38Z)
- Approved: "tag it." (2026-10-16) — finish the release in this session after the retire: PR, merge on green CI, tag v0.80.0, verify the GitHub release
- Approved: "Push, delete branch (Recommended)" (2026-10-16) — push the close-out commit (plan Now + gotcha) to main and delete the superseded local `release/v0.80.0` branch
- Approved: "lets gooo" (2026-10-16) — start the follow-ups #700, #701, #702
- Compactions: 1

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let `provider: openai` nodes run on the user's ChatGPT subscription, after a one-time device-code sign-in from `tracker setup` that uses Codex's OAuth client.

**Architecture:** A new `internal/chatgptauth` package owns sign-in, the token file and refresh. `llm/openai` gains a ChatGPT mode (`WithChatGPTAuth`) that reuses the Responses translation and SSE parser and sends to `chatgpt.com/backend-api/codex/responses`. With `TRACKER_OPENAI_AUTH=chatgpt`, `buildClient` registers openai from the token file, and its responses carry the usage label `openai-subscription`, which prices at $0.

**Tech Stack:** Go (go1.26.6 toolchain; plain `go` works in this repo), `net/http` + `httptest`, Bubble Tea + bubbles (setup TUI), `golang.org/x/sys/unix` (flock; already in go.mod).

**Spec:** `docs/superpowers/specs/2026-10-15-chatgpt-subscription-design.md` (decisions D1–D7). The protocol facts and their openai/codex file:line citations are in `docs/superpowers/specs/2026-10-15-chatgpt-subscription-research.md`. Read both before any task.

## Global Constraints

- OAuth client ID `app_EMoamEEZ73f0CkXaXp7hrann`; issuer `https://auth.openai.com`.
- Device code: `POST {issuer}/api/accounts/deviceauth/usercode` with JSON `{"client_id"}`; `POST {issuer}/api/accounts/deviceauth/token` with JSON `{"device_auth_id","user_code"}` (403/404 = still pending); show `{issuer}/codex/device`; give up after 15 minutes; exchange at `POST {issuer}/oauth/token`, form-encoded, `redirect_uri={issuer}/deviceauth/callback`, with the server-issued `code_verifier`.
- Refresh: `POST {issuer}/oauth/token`, JSON `{"grant_type":"refresh_token","client_id","refresh_token"}`. Refresh when the access token's `exp` is within 5 minutes; when `exp` won't parse, when `last_refresh` is over 8 days old. Permanent failures: codes `refresh_token_expired`, `refresh_token_reused`, `refresh_token_invalidated`; any 401; 400 `invalid_grant`.
- Backend: `POST https://chatgpt.com/backend-api/codex/responses`. Headers `Authorization: Bearer <access>`, `ChatGPT-Account-ID`, `originator: tracker`, `User-Agent: tracker/<version> (<GOOS>; <GOARCH>)`, `session-id`. Never send `OpenAI-Beta`, Codex's originator or Codex's user agent.
- Body in ChatGPT mode: `store:false`, `stream:true`, `include:["reasoning.encrypted_content"]`, `prompt_cache_key=<session-id>`; remove `max_output_tokens`, `temperature`, `top_p`, `stop`.
- Switch `TRACKER_OPENAI_AUTH` = `chatgpt` | `api-key`; unset means `api-key`. envpolicy row: `NetworkDestination`, `ConfigEnv`, since `v0.80.0`.
- Token file `$XDG_CONFIG_HOME/tracker/chatgpt-auth.json` (default `~/.config/tracker/`), mode 0600 in a 0700 dir, written atomically; lock file `chatgpt-auth.lock` beside it.
- Usage label `openai-subscription`, priced at $0 with no unknown-model warning.
- Tokens never enter the process environment, a log line, an event or an error string.
- In ChatGPT mode the bearer goes only to chatgpt.com and the refresh token only to auth.openai.com: `OPENAI_BASE_URL`, `TRACKER_GATEWAY_URL`, `--gateway-url` and `--gateway-kind` never apply to openai.
- Repo rules: each new hand-written Go file starts with two `// ABOUTME:` lines; TDD; `--no-verify` is forbidden; `make complexity` stays green (put new logic in small functions and files); conventional commits ending with `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`; `git add` named files only, never `-A`; `make ci` is the canonical check.
- Work happens on `feat/chatgpt-subscription`. Nobody pushes or opens a PR until Doctor Biz says so.

## Review Focus

1. An access token already expired when loaded (a laptop asleep for days): the first request refreshes first instead of taking a 401 round trip. → Task 5, `TestTokenSourceRefreshesExpiredTokenBeforeFirstUse`.
2. A token file missing `account_id` or `refresh_token` (hand-edited, or written by an older build): a clear "sign in again" error, never an empty header or a panic. → Task 3, `TestLoadRejectsIncompleteTokens`.
3. A device-code `interval` sent as `"5"`, as `5`, or not at all: poll at that interval, 5 s when absent or not positive. → Task 4, `TestStartDeviceLoginParsesInterval`.
4. Esc on the sign-in screen: polling stops, no token file is written, and the wizard returns to the OpenAI page. → Task 8, `TestSetupChatGPTSigninEscCancelsPolling`.
5. Parallel branches all get a 401 for the same revoked token: one refresh; every branch retries with the new token; a permanent failure reaches all of them as the sign-in-again error, with one network call. → Task 5, `TestForceRefreshSameRejectedTokenRefreshesOnce`.

---

### Task 1: Shared XDG config-home resolver

**Files:**
- Create: `internal/xdgconfig/xdgconfig.go`, `internal/xdgconfig/xdgconfig_test.go`
- Modify: `cmd/tracker/config_env.go` (`xdgConfigHome`, ~lines 36–53)

**Interfaces:**
- Produces: `func ConfigHome() (dir, ignored string, err error)` in package `xdgconfig`. It returns `$XDG_CONFIG_HOME` when that is absolute, else `$HOME/.config`; `ignored` is the relative `XDG_CONFIG_HOME` value it skipped, or "".

- [ ] **Step 1: Write the failing tests**
  - `TestConfigHomeAbsoluteXDG`: `t.Setenv("XDG_CONFIG_HOME", abs)` → `dir == abs`, `ignored == ""`.
  - `TestConfigHomeRelativeXDGIgnored`: XDG `"rel/dir"`, HOME `tmp` → `dir == filepath.Join(tmp, ".config")`, `ignored == "rel/dir"`.
  - `TestConfigHomeUnsetUsesHome`: XDG `""` → `filepath.Join(home, ".config")`.
- [ ] **Step 2: Run** `go test ./internal/xdgconfig/ -count=1` — expect FAIL (package missing).
- [ ] **Step 3: Implement `ConfigHome`.** Then make `cmd/tracker/config_env.go`'s `xdgConfigHome()` call it, keeping its existing `envNotice("ignoring relative XDG_CONFIG_HOME=%q (must be absolute); using $HOME/.config", ignored)` when `ignored != ""`.
- [ ] **Step 4: Run** `go test ./internal/xdgconfig/ ./cmd/tracker/ -count=1 -short` — expect PASS, with existing config_env tests unchanged.
- [ ] **Step 5: Commit** `refactor: move the XDG config-home resolver into internal/xdgconfig`.

### Task 2: The `TRACKER_OPENAI_AUTH` switch

**Files:**
- Create: `internal/chatgptauth/mode.go`, `internal/chatgptauth/mode_test.go`
- Modify: `internal/envpolicy/table.go` (config-env tier), `cmd/tracker/config_env.go` (`buildProviderEnvKeys`), `docs/architecture/env-policy.md` (config tier row), `site/content/cli.html` (env table near the `TRACKER_GATEWAY_KIND` row, ~line 416; the config `.env` bullet, ~line 395)
- Test: `cmd/tracker/envfiles_test.go`, `cmd/tracker/config_env_test.go`

**Interfaces:**
- Produces (package `chatgptauth`):
  ```go
  const EnvVar = "TRACKER_OPENAI_AUTH"
  type Mode string
  const (
      ModeAPIKey  Mode = "api-key"
      ModeChatGPT Mode = "chatgpt"
  )
  func ParseMode(v string) (Mode, error) // trims space; "" → ModeAPIKey; any other value → error naming both valid values
  func ModeFromEnv() (Mode, error)       // ParseMode(os.Getenv(EnvVar))
  ```

- [ ] **Step 1: Write the failing tests**
  - `TestParseMode` (table): `""`→api-key, `"api-key"`→api-key, `"chatgpt"`→chatgpt, `" chatgpt "`→chatgpt; `"ChatGPT"` and `"subscription"` → error whose text contains both `chatgpt` and `api-key`.
  - `TestModeEnvVarRegisteredConfigTier`: `envpolicy.AllowedFrom(EnvVar, envpolicy.ConfigEnv)` true; `envpolicy.AllowedFrom(EnvVar, envpolicy.ProjectEnv)` false; `envpolicy.Lookup(EnvVar)` has `Purpose == envpolicy.NetworkDestination`.
  - `TestProjectEnvCannotSetOpenAIAuth` (envfiles_test.go; copy the shape of the existing project-tier skip test there): a project `.env` with `TRACKER_OPENAI_AUTH=chatgpt` leaves `os.Getenv` empty and reports the name as ignored; a config `.env` with the same line sets it.
  - `TestSetupMayWriteOpenAIAuth` (config_env_test.go): `mergeProviderEnv(map[string]string{}, map[string]string{"TRACKER_OPENAI_AUTH": "chatgpt"})` keeps the entry.
- [ ] **Step 2: Run** `go test ./internal/chatgptauth/ ./cmd/tracker/ -run 'TestParseMode|TestModeEnvVar|TestProjectEnvCannotSetOpenAIAuth|TestSetupMayWriteOpenAIAuth' -count=1` — expect FAIL.
- [ ] **Step 3: Implement.** `mode.go`; in `table.go`'s config tier add `v("TRACKER_OPENAI_AUTH", NetworkDestination, ConfigEnv, "v0.80.0", "openai auth: api-key (default) or chatgpt — bill openai to your ChatGPT subscription after signing in with tracker setup")`; add `chatgptauth.EnvVar` to `buildProviderEnvKeys`.
- [ ] **Step 4: Docs.** In env-policy.md, list the name in the config tier. In cli.html, add an env-table row (default `api-key`, the two values, "Config `.env` or shell", badge `v0.80.0`) and name it in the config `.env` bullet.
- [ ] **Step 5: Run** the Step 2 command plus `go test ./internal/envpolicy/ -count=1` (`TestEveryGetenvLiteralIsRegistered`) — expect PASS.
- [ ] **Step 6: Commit** `feat: add the TRACKER_OPENAI_AUTH switch (config tier)`.

### Task 3: Claims, the token file, and status

**Files:**
- Create in `internal/chatgptauth/`: `claims.go`, `store.go`, `store_unix.go`, `store_windows.go`, `status.go`, `claims_test.go`, `store_test.go`, `status_test.go`, `jwt_test.go` (test helper `fakeJWT(claims map[string]any) string`)

**Interfaces:**
- Consumes: `xdgconfig.ConfigHome` (Task 1).
- Produces:
  ```go
  type Tokens struct {
      IDToken      string    `json:"id_token"`
      AccessToken  string    `json:"access_token"`
      RefreshToken string    `json:"refresh_token"`
      AccountID    string    `json:"account_id"`
      LastRefresh  time.Time `json:"last_refresh"`
  }
  type Identity struct{ AccountID, PlanType string }
  func ParseIDToken(idToken string) (Identity, error)          // claims["https://api.openai.com/auth"].{chatgpt_account_id, chatgpt_plan_type}
  func AccessTokenExpiry(accessToken string) (time.Time, bool)  // the `exp` claim
  const FileName = "chatgpt-auth.json"
  const LockFileName = "chatgpt-auth.lock"
  func DefaultPath() (string, error)                           // ConfigHome()/tracker/FileName
  var ErrNotSignedIn = errors.New("not signed in to ChatGPT; run `tracker setup` and choose OpenAI → ChatGPT subscription")
  type UnsafeFileError struct{ Path, Problem string }           // Error(): "refusing <Path>: <Problem>; fix with: chmod 600 <Path>"
  func Load(path string) (Tokens, error)
  func Save(path string, t Tokens) error
  type Status struct {
      SignedIn                  bool
      PlanType                  string
      LastRefresh, AccessExpiry time.Time
  }
  func ReadStatus(path string) (Status, error) // ErrNotSignedIn → Status{}, nil
  ```
  - JWT decode: split on `.`; require three non-empty parts; decode part 2 with `base64.RawURLEncoding` (accept padded input too); `json.Unmarshal`. No signature check, matching Codex (spec §1).
  - `Load`: a missing file returns `ErrNotSignedIn`. A symlink, or on Unix any group/other mode bit, returns `*UnsafeFileError`. On Windows, follow `cmd/tracker/envfiles_windows.go`: `Lstat` symlink check only. Empty access, refresh or account fields return an error containing "sign in again" and no token value.
  - `Save`: `MkdirAll(dir, 0o700)`; write a temp file at 0600 in the same dir; fsync; rename.

- [ ] **Step 1: Write the failing tests**
  - `TestParseIDTokenReadsAccountAndPlan`: auth claim `{"chatgpt_account_id":"acct-1","chatgpt_plan_type":"plus"}` → `Identity{"acct-1","plus"}`.
  - `TestParseIDTokenRejectsMalformed`: `"a.b"`, `""`, `"x..y"` → error.
  - `TestAccessTokenExpiry`: `exp: 1760000000` → `time.Unix(1760000000, 0)`, true; no `exp` → false.
  - `TestSaveWritesPrivateFileAtomically`: Save into `t.TempDir()+"/sub/"+FileName` → file 0600, dir 0700, no other files in the dir, and `Load` round-trips equal.
  - `TestLoadMissingIsNotSignedIn`: `errors.Is(err, ErrNotSignedIn)`.
  - `TestLoadRefusesSymlink` and `TestLoadRefusesGroupReadable` (both `//go:build !windows`): `*UnsafeFileError`; the second's text contains `chmod 600`.
  - `TestLoadRejectsIncompleteTokens` (Review Focus 2): no `account_id` → error contains "sign in again" and not the access-token string.
  - `TestReadStatus`: signed in → `SignedIn`, `PlanType "plus"`, `AccessExpiry` from `exp`; missing file → `SignedIn false`, nil error.
  - `TestDefaultPathUsesXDG`: absolute XDG → `<xdg>/tracker/chatgpt-auth.json`.
- [ ] **Step 2: Run** `go test ./internal/chatgptauth/ -count=1` — expect FAIL.
- [ ] **Step 3: Implement** the files above.
- [ ] **Step 4: Run** `go test ./internal/chatgptauth/ -count=1 -race` — expect PASS.
- [ ] **Step 5: Commit** `feat(chatgptauth): token file, claims and status`.

### Task 4: Device-code sign-in

**Files:**
- Create in `internal/chatgptauth/`: `client.go`, `device.go`, `useragent.go`, `device_test.go`, `useragent_test.go`, `authserver_test.go` (fake auth.openai.com shared with Task 5)

**Interfaces:**
- Consumes: `Tokens`, `ParseIDToken` (Task 3).
- Produces:
  ```go
  const Issuer = "https://auth.openai.com"
  const ClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
  type AuthClient struct {
      Issuer        string        // default Issuer
      HTTPClient    *http.Client  // default: 30 s timeout
      DeviceTimeout time.Duration // default 15 * time.Minute
      UserAgent     string        // default UserAgent()
  }
  func NewAuthClient() *AuthClient
  func UserAgent() string // "tracker/<version> (<GOOS>; <GOARCH>)"
  type DeviceCode struct {
      VerificationURL string        // Issuer + "/codex/device"
      UserCode        string
      Interval        time.Duration // server value; 5 s when absent or <= 0
      ExpiresAt       time.Time     // start + DeviceTimeout
      deviceAuthID    string
  }
  var ErrDeviceCodeDisabled = errors.New("device-code sign-in is not enabled for this account (OpenAI returned 404)")
  var ErrDeviceCodeExpired = errors.New("the sign-in code expired after 15 minutes; start again")
  func (c *AuthClient) StartDeviceLogin(ctx context.Context) (DeviceCode, error)
  func (c *AuthClient) WaitForDeviceLogin(ctx context.Context, dc DeviceCode) (Tokens, error)
  ```
  - `UserAgent` version: `debug.ReadBuildInfo()` → `Main.Version` when `Main.Path` is `github.com/2389-research/tracker`, else that module's entry in `Deps`, else `dev`; `(devel)` also becomes `dev`.
  - Usercode response: `device_auth_id`, `user_code` or `usercode`, `interval` as a string or a number.
  - Poll: 200 → `{authorization_code, code_challenge, code_verifier}`; 403 or 404 → wait `Interval` (never past `ExpiresAt`) and retry; any other status → `fmt.Errorf("device sign-in failed: HTTP %d", status)` with no body text. Wait in a `select` on `ctx.Done()` and a timer.
  - Exchange form fields: `grant_type=authorization_code`, `client_id`, `code`, `redirect_uri=<Issuer>/deviceauth/callback`, `code_verifier`. The response must carry `id_token`, `access_token` and `refresh_token`. Fill `AccountID` from `ParseIDToken`, and set `LastRefresh = now`.
  - Every request sends `User-Agent`.
  - Fake server `newFakeAuthServer(t)`: counters per endpoint; configurable pending polls, usercode 404, interval value, token response; records bodies and headers.

- [ ] **Step 1: Write the failing tests**
  - `TestDeviceLoginHappyPath`: 2 pending polls, then success → tokens set, `AccountID "acct-1"`. The server saw `client_id=app_EMoamEEZ73f0CkXaXp7hrann`, `redirect_uri=<srv>/deviceauth/callback`, the server-issued verifier, a form content type, and a `User-Agent` starting `tracker/`.
  - `TestStartDeviceLoginDisabled`: usercode 404 → `errors.Is(err, ErrDeviceCodeDisabled)`.
  - `TestStartDeviceLoginParsesInterval` (Review Focus 3): `"7"` → 7 s; `3` → 3 s; absent → 5 s; the `"usercode"` spelling is accepted.
  - `TestWaitForDeviceLoginExpires`: `DeviceTimeout` 50 ms, always pending → `ErrDeviceCodeExpired`.
  - `TestWaitForDeviceLoginHonorsCancel`: cancel during polling → returns `ctx.Err()` within 1 s, and the server's poll count stops rising.
  - `TestDeviceLoginErrorsNeverCarryTokens`: an exchange response missing `id_token` → the error excludes every token string.
  - `TestUserAgentShape`: matches `^tracker/\S+ \(\w+; \w+\)$`.
- [ ] **Step 2: Run** `go test ./internal/chatgptauth/ -run 'Device|UserAgent' -count=1` — expect FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./internal/chatgptauth/ -count=1 -race` — expect PASS.
- [ ] **Step 5: Commit** `feat(chatgptauth): device-code sign-in`.

### Task 5: Refresh and the token source

**Files:**
- Create in `internal/chatgptauth/`: `refresh.go`, `source.go`, `lock_unix.go`, `lock_windows.go`, `refresh_test.go`, `source_test.go`, `source_race_test.go` (`//go:build !windows`)

**Interfaces:**
- Consumes: `Load`, `Save`, `AccessTokenExpiry`, `ParseIDToken` (Task 3); `AuthClient` and the fake server (Task 4).
- Produces:
  ```go
  type RefreshError struct {
      Code      string // server error code, e.g. "refresh_token_reused"; "" if none
      Status    int    // 0 for a network failure
      Permanent bool
  }
  // Error(): permanent → "ChatGPT sign-in expired or was revoked (<Code, or "HTTP <Status>">); run `tracker setup` to sign in again"
  //          transient → "refreshing the ChatGPT sign-in failed: HTTP <Status>" (or the network error)
  func (e *RefreshError) Temporary() bool // !Permanent
  func (c *AuthClient) Refresh(ctx context.Context, refreshToken string) (Tokens, error) // sets only the fields the server returned
  type TokenSource struct{ /* path, client, mu sync.Mutex, cached Tokens, permanent error, now func() time.Time */ }
  func NewTokenSource(path string, client *AuthClient) *TokenSource
  func (s *TokenSource) Token(ctx context.Context) (accessToken, accountID string, err error)
  func (s *TokenSource) ForceRefresh(ctx context.Context, rejected string) error
  ```
  - `*TokenSource` satisfies `openai.ChatGPTCredentials` (Task 7) structurally.
  - Classify a refresh failure from the JSON error body. The error-body shape is in research §C (`manager.rs:1680-1708`); check it there at implementation. It's permanent for 401, for the three codes above, or for 400 with `invalid_grant`; everything else, network errors included, is transient.
  - After a permanent failure, the source returns that error without network calls until the token file's refresh token changes (setup signed in again).

  The algorithm, which the signatures don't pin down:
  ```
  Token(ctx):           lock s.mu
                        if s.cached is zero: s.cached = Load(path)
                        if !needsRefresh(s.cached, now): return cached access, account
                        refreshLocked(ctx, s.cached.AccessToken); return cached values
  ForceRefresh(ctx, r): _, err := s.forceRefresh(ctx, r); return err
  forceRefresh(ctx, r) (refreshed bool, err error): lock s.mu; return refreshLocked(ctx, r)   // unexported; the race test's children call it
  refreshLocked(ctx, stale) (refreshed bool, err error):
      unlock := lockFile(ctx, dir/LockFileName)  // unix: flock LOCK_EX|LOCK_NB, retried every 50 ms until ctx is done; windows: no-op
      defer unlock()
      fresh := Load(path)
      if fresh.AccessToken != stale && !needsRefresh(fresh, now): s.cached = fresh; return false, nil   // another process refreshed
      if s.permanent != nil && fresh.RefreshToken == the token that failed: return false, s.permanent
      got := client.Refresh(ctx, fresh.RefreshToken)   // permanent error → remember it in s.permanent
      merged := fresh overlaid with got's non-empty fields; merged.LastRefresh = now
      if merged.AccountID == "": take it from ParseIDToken(merged.IDToken)
      Save(path, merged); s.cached = merged; return true, nil
  needsRefresh(t, now): exp, ok := AccessTokenExpiry(t.AccessToken)
      ok  → !now.Add(5*time.Minute).Before(exp)
      !ok → now.Sub(t.LastRefresh) > 8*24*time.Hour
  ```

- [ ] **Step 1: Write the failing tests**
  - `TestRefreshSendsJSONGrant`: body exactly `{"grant_type":"refresh_token","client_id":"app_EMoamEEZ73f0CkXaXp7hrann","refresh_token":"r-1"}`, JSON content type.
  - `TestRefreshClassifiesPermanentFailures` (table): the three codes (400 and 401), 401 without a code, and 400 `invalid_grant` → `Permanent`, and `Error()` contains `tracker setup`; 500 and a closed server → not permanent. In every row, `Error()` never contains the refresh token `r-1`.
  - `TestTokenSourceReturnsFreshTokenWithoutRefresh`: `exp` now+1h → zero refresh calls.
  - `TestTokenSourceRefreshesNearExpiry`: `exp` now+4m → one refresh; the file holds the rotated `r-2` and a new `LastRefresh`, and is still 0600.
  - `TestTokenSourceRefreshesExpiredTokenBeforeFirstUse` (Review Focus 1): `exp` in the past → `Token()` returns `a-2` after exactly one refresh call.
  - `TestTokenSourceKeepsRefreshTokenWhenNotRotated`: a response without `refresh_token` → the stored refresh token stays `r-1`.
  - `TestTokenSourceEightDayFallback`: an access token without `exp` and `LastRefresh` 9 days old → refresh; 1 day old → none.
  - `TestForceRefreshSkipsWhenFileAlreadyNewer`: the file holds a fresh `a-2`; `ForceRefresh(ctx, "a-1")` → zero refresh calls, and `Token()` returns `a-2`.
  - `TestForceRefreshSameRejectedTokenRefreshesOnce` (Review Focus 5): 8 goroutines behind a start barrier call `ForceRefresh(ctx, "a-1")` → one refresh call, all nil, `Token() == "a-2"`. Variant: the server answers `refresh_token_invalidated` → all 8 get a permanent `*RefreshError`, with one refresh call in total.
  - `TestTokenSourceCrossProcessRefreshOnce` (source_race_test.go).
    - Setup: `TestMain` runs child mode when `TRACKER_CHATGPTAUTH_RACE_CHILD=1`. That env read is in a `_test.go` file, which the getenv registry scan skips.
    - The fake refresh handler sleeps 200 ms, rotates `r-1`→`r-2`, and answers a reused `r-1` with 400 `refresh_token_reused`.
    - The parent starts **2** child processes (state this count in the test's doc comment). Each runs 4 goroutines that wait on a start-file barrier, call the unexported `refreshLocked` path through `forceRefresh`, and print `refreshed` or `skipped`.
    - Assert: refresh calls served == 1; `refresh_token_reused` responses == 0; across the children `refreshed == 1` and `skipped == 7`; the file's access token is `a-2`.
    - Every failure message includes each child's exit code and stderr.
- [ ] **Step 2: Run** `go test ./internal/chatgptauth/ -run 'Refresh|TokenSource|ForceRefresh' -count=1` — expect FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./internal/chatgptauth/ -count=1 -race`, then `go test ./internal/chatgptauth/ -run 'CrossProcess|SameRejected' -count=20 -race` — expect PASS on all 20.
- [ ] **Step 5: Commit** `feat(chatgptauth): token refresh with in-process and file locks`.

### Task 6: llm — usage-limit error, subscription label, keyless providers

**Files:**
- Modify: `llm/errors.go` (non-retryable section), `llm/pricing.go` (`EstimateCostForProvider`), `llm/provider.go`, `llm/client.go` (`NewClientFromEnv`; the `resp.Provider = adapter.Name()` lines, ~247 and ~304)
- Test: `llm/errors_test.go`, `llm/pricing_test.go`, `llm/token_tracker_test.go`, `llm/client_test.go`

**Interfaces:**
- Produces:
  ```go
  type UsageLimitError struct {
      ProviderError
      ResetsAt time.Time // zero when unknown
      PlanType string
  }
  func (e *UsageLimitError) Retryable() bool // false
  const OpenAISubscription = "openai-subscription"
  func IsSubscriptionUsage(provider string) bool
  type UsageLabeler interface{ UsageLabel() string }
  func (c *Client) UsageLabel(provider string) string
  func NewClientFromEnvWith(constructors map[string]func(apiKey string) (ProviderAdapter, error), extra ...ProviderAdapter) (*Client, error)
  ```
  - `Client.UsageLabel` returns the label from the adapter registered under that name, when it implements `UsageLabeler` with a non-empty label; otherwise the name itself, and `""` for `""`.
  - `NewClientFromEnv(c)` becomes `NewClientFromEnvWith(c)`. An extra adapter registers under its `Name()`, replaces an env-built adapter of that name (its constructor is never called), and takes that name's slot in `providerPriority` when the default is picked.
  - Both `Complete` paths stamp `resp.Provider` with the adapter's usage label.
  - `EstimateCostForProvider` returns 0 for `IsSubscriptionUsage(provider)`, before any lookup and without a warning.

- [ ] **Step 1: Write the failing tests**
  - `TestUsageLimitErrorIsPausableNotBilling`: message `openai: ChatGPT usage limit reached (plus plan), resets at 2026-10-15T20:00:00Z` → `IsUsageLimit` true; `UsageLimitResetAt` equals that time; `IsBillingError` false; `isRetryable` false.
  - `TestEstimateCostForProviderSubscriptionIsFree`: `("openai-subscription", m, 1M in/out)` → 0, where `m` is a model with `IsPriced(m)` true; `("openai", m, …)` > 0.
  - `TestTokenTrackerPricesSubscriptionAtZero`: `AddUsage("openai-subscription", u, m)` → `CostByProvider(nil)["openai-subscription"].USD == 0`, with tokens counted.
  - `TestClientStampsUsageLabel`: a stub adapter named `openai` labeled `openai-subscription` → `resp.Provider == "openai-subscription"` on the plain path and the traced path (a request with `TraceObservers`); a `TokenTracker` middleware buckets it under the label. A stub without the method → `openai`.
  - `TestClientUsageLabel`: labeler → label; unregistered `gemini` → `gemini`; `""` → `""`.
  - `TestNewClientFromEnvWithKeylessExtra`: no env keys plus an extra `openai` → builds, and `DefaultProvider() == "openai"`.
  - `TestNewClientFromEnvWithExtraTakesPriority`: `GEMINI_API_KEY` plus extra `openai` → default `openai`; `ANTHROPIC_API_KEY` plus extra `openai` → default `anthropic`.
  - `TestNewClientFromEnvWithExtraReplacesEnvBuilt`: `OPENAI_API_KEY` set plus extra `openai` → the openai constructor's call counter stays 0.
- [ ] **Step 2: Run** `go test ./llm/ -count=1 -run 'UsageLimitError|Subscription|UsageLabel|FromEnvWith'` — expect FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./llm/ -count=1 -race` and `go test ./cmd/tracker-conformance -run TestGoldenTraces -count=1` — expect PASS with no golden drift (every existing adapter's label equals its name).
- [ ] **Step 5: Commit** `feat(llm): usage-limit error, subscription usage label, keyless provider registration`.

### Task 7: openai adapter — ChatGPT mode

**Files:**
- Create: `llm/openai/chatgpt.go`, `llm/openai/chatgpt_test.go`
- Modify: `llm/openai/adapter.go` (struct field; `Complete`; `streamRequest`; the `setHeaders` call sites)

**Interfaces:**
- Consumes: `llm.UsageLimitError`, `llm.OpenAISubscription`, `llm.UsageLabeler`, `llm.NewStreamAccumulator` (Task 6).
- Produces:
  ```go
  type ChatGPTCredentials interface {
      Token(ctx context.Context) (accessToken, accountID string, err error)
      ForceRefresh(ctx context.Context, rejected string) error
  }
  const ChatGPTBaseURL = "https://chatgpt.com/backend-api/codex"
  func WithChatGPTAuth(creds ChatGPTCredentials, userAgent string) Option
  func (a *Adapter) UsageLabel() string // llm.OpenAISubscription in ChatGPT mode, "openai" otherwise
  ```
  - `WithChatGPTAuth` sets the base URL to `ChatGPTBaseURL`, the path to `/responses`, and a per-adapter `session-id` of 32 hex chars from `crypto/rand`. A later `WithBaseURL` overrides the URL; only tests and Task 10's swap variable do that.
  - `Name()` stays `openai`.
  - Body: `translateRequest`, then `reshapeForChatGPT(body []byte, sessionID string) ([]byte, error)`, which unmarshals to a map and sets or deletes the Global Constraints fields.
  - `setChatGPTHeaders(req *http.Request, access, account string)`: Content-Type, Authorization, `ChatGPT-Account-ID` (omitted when empty), `originator: tracker`, User-Agent and `session-id`. `WithExtraHeaders` headers are gateway auth and are not applied in this mode.
  - `sendChatGPT(ctx, body) (*http.Response, error)`: get the token and send. On a 401: close the body, `ForceRefresh(ctx, access)`, get the token again and retry once. A second 401 returns `&llm.AuthenticationError{…Msg: "openai: ChatGPT rejected the refreshed sign-in; run `tracker setup` to sign in again", Provider: "openai", StatusCode: 401}`.
  - A credentials error becomes `&llm.NetworkError` (retryable) when it has `Temporary() bool` returning true; otherwise `&llm.ConfigurationError{SDKError{Msg: "openai: " + err.Error(), Cause: err}}`, which is fatal.
  - `chatgptStatusError(status int, body []byte, h http.Header) error`:
    - 429 with JSON `error.type == "usage_limit_reached"` → `&llm.UsageLimitError{…, Provider: "openai", StatusCode: 429, ErrorCode: "usage_limit_reached", ResetsAt: time.Unix(resets_at, 0).UTC(), PlanType}`. Message: `openai: ChatGPT usage limit reached`, plus ` (<plan> plan)` when known, plus `, resets at <RFC3339>` when `resets_at > 0`.
    - 429 with `error.type == "usage_not_included"` → `&llm.AccessDeniedError{…Msg: "openai: your ChatGPT plan does not include Codex usage"}`.
    - Anything else → `llm.ErrorFromStatusCodeRetryAfter(status, string(body), "openai", llm.ParseRetryAfter(h))`.
  - `Complete` in this mode reads `a.Stream(ctx, req)` into `llm.NewStreamAccumulator()` and returns the first `evt.Err`.
  - `Stream` keeps its goroutine. `streamRequest` uses the ChatGPT body, send and error mapping when ChatGPT mode is on.
  - `response.metadata` already falls to `EventProviderEvent` in `handleSSEDataOtherEvents`; pin that with a test.
  - Tests use an httptest server as the backend, plus a fake credentials struct in the test file that counts calls. The credentials are our own interface; the server stands in for the external service.

- [ ] **Step 1: Write the failing tests**
  - `TestChatGPTRequestShape`: send `Request{Model: "gpt-5.6-sol", Messages: system+user, Tools: [one], MaxTokens: 100, Temperature: 0.2, TopP: 0.9, StopSequences: ["X"]}` through `Stream`.
    - The server sees path `/responses`.
    - The body has `store:false`, `stream:true`, `include:["reasoning.encrypted_content"]`, and `prompt_cache_key` equal to the `session-id` header. It has `instructions`, `model`, `input` and `tools`, and lacks `max_output_tokens`, `temperature`, `top_p` and `stop`.
    - Headers: `Bearer a-1`, `ChatGPT-Account-ID: acct-1`, `originator: tracker`, the given User-Agent, and a 32-hex `session-id`. No `OpenAI-Beta`.
  - `TestChatGPTSessionIDStable`: two requests from one adapter share a `session-id`; a second adapter's differs.
  - `TestChatGPTCompleteStreams`: the server streams created → text delta `hi` → completed with usage → `Complete` returns `hi` with usage; the request had `stream:true`.
  - `TestChatGPTIgnoresMetadataEvent`: a stream containing `event: response.metadata` with a `safety_buffering` payload → no error, and the text is intact.
  - `TestChatGPTRefreshesOnceOn401`: 401, then 200 → `ForceRefresh` called once with `a-1`; the retry carries `Bearer a-2`.
  - `TestChatGPTSecond401Fails`: 401 twice → `*llm.AuthenticationError` mentioning `tracker setup`; `ForceRefresh` called once.
  - `TestChatGPTUsageLimitPausesNotRetries`: a 429 with body `{"error":{"type":"usage_limit_reached","plan_type":"plus","resets_at":1760558400}}`.
    - The error is a `*llm.UsageLimitError`; `llm.IsUsageLimit` is true; `UsageLimitResetAt == time.Unix(1760558400, 0).UTC()`; `IsBillingError` is false.
    - Through `llm.NewClient(llm.WithProvider(a))` plus `llm.NewRetryMiddleware(llm.WithMaxRetries(3))`, the server sees exactly 1 request.
  - `TestChatGPTUsageNotIncluded`: → `*llm.AccessDeniedError` with that message.
  - `TestChatGPTCredentialErrors`: credentials returning a plain error → `*llm.ConfigurationError` with the message kept; returning an error whose `Temporary()` is true → `*llm.NetworkError`.
  - `TestChatGPTErrorsNeverCarryToken`: with access `SECRET-a-1`, the 401×2, 429 and 500 paths → no error string contains `SECRET`.
  - `TestChatGPTUsageLabel`: ChatGPT mode → `openai-subscription`; API-key mode → `openai`; `Name()` is `openai` in both.
  - `TestAPIKeyModeStillSendsMaxOutputTokens`: API-key mode still posts `/v1/responses` with `max_output_tokens`.
- [ ] **Step 2: Run** `go test ./llm/openai/ -run ChatGPT -count=1` — expect FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./llm/openai/ -count=1 -race` and `make complexity` — expect PASS, with the existing adapter tests unchanged.
- [ ] **Step 5: Commit** `feat(openai): ChatGPT-subscription mode for the Responses adapter`.

### Task 8: Setup wizard — Auth choice and device-code sign-in

**Files:**
- Create: `cmd/tracker/setup_chatgpt.go`, `cmd/tracker/setup_chatgpt_test.go`
- Modify: `cmd/tracker/setup_model.go` (OpenAI config page, `collectValues`, help line, top-level esc routing), `cmd/tracker/setup.go` (`runSetupUI`: real login, auth path, status), `README.md` (setup section, ~line 308), `site/content/cli.html` (`tracker setup` block, ~line 196)

**Interfaces:**
- Consumes: `chatgptauth.EnvVar`, `ModeAPIKey`, `ModeChatGPT`, `ParseMode`, `DefaultPath`, `ReadStatus`, `Status`, `Save`, `NewAuthClient`, `DeviceCode`, `Tokens`, `ErrDeviceCodeDisabled`.
- Produces (package `main`):
  ```go
  type chatgptLoginer interface {
      StartDeviceLogin(ctx context.Context) (chatgptauth.DeviceCode, error)
      WaitForDeviceLogin(ctx context.Context, dc chatgptauth.DeviceCode) (chatgptauth.Tokens, error)
  }
  type chatgptCodeMsg struct{ code chatgptauth.DeviceCode; err error }
  type chatgptDoneMsg struct{ status chatgptauth.Status; err error }
  // setupModel gains: openaiAuth chatgptauth.Mode; chatgptStatus chatgptauth.Status;
  // login chatgptLoginer; authPath string; sign-in state (idle|requesting|waiting),
  // the device code, the last error, and the sign-in cancel func.
  ```
  Behavior (spec §3, D5):
  - **The OpenAI page** has a first row: `Auth  (•) ChatGPT subscription  ( ) API key`. The choice starts on ChatGPT subscription when `existing[EnvVar]` parses to `chatgpt`, otherwise on API key. Focus starts on that row, and space, ←, → toggle it.
  - **API key** shows today's API Key and Base URL fields; tab cycles Auth → API Key → Base URL.
  - **ChatGPT subscription** shows a status line: `plus plan · refreshed 2h ago` (plan left out when empty; age via `formatElapsed`), or `not signed in`. Help line when not signed in: `space switch  enter sign in  esc cancel`. When signed in: `space switch  enter continue  s sign in again  esc cancel`.
    - This deviates from the approved preview, which said "enter: sign in again": enter has to continue when you're already signed in, or every setup run forces a new sign-in.
  - **Starting sign-in** (enter when not signed in, or `s`): Cmd 1 runs `StartDeviceLogin`. Cmd 2 runs `WaitForDeviceLogin`, then `chatgptauth.Save(authPath, tokens)`, then `ReadStatus`, all inside the Cmd so no file IO happens on the UI goroutine. The ctx comes from `context.WithCancel` and is kept on the model.
  - **The sign-in page copy** (exact):
    ```
    Sign in with ChatGPT

    1. On any device, open:
       <VerificationURL>
    2. Enter this code (expires in 15 min):
       <UserCode>

    <spinner> waiting for you to finish…

    Signs in through Codex's OAuth client — for your own ChatGPT account on your own machine.
    Continue only if you started this sign-in in tracker.

    esc cancel sign-in
    ```
  - **Esc on the sign-in page** cancels the ctx and returns to the OpenAI page; the wizard keeps running. Top-level `Update` must route esc there before the global cancel. Ctrl+C still cancels the whole wizard, and the ctx with it.
  - **On success** the OpenAI page shows `✓ signed in · <plan> plan`, and enter continues. **On failure** it shows `sign-in failed: <err>`, using the `ErrDeviceCodeDisabled` text as is.
  - **`collectValues`**: OpenAI enabled with ChatGPT chosen → `values[EnvVar] = "chatgpt"`, and the OPENAI_API_KEY and OPENAI_BASE_URL inputs aren't collected. API key chosen while `existing[EnvVar]` was `chatgpt` → `values[EnvVar] = "api-key"`. Otherwise no entry.
  - With ChatGPT chosen but not signed in, enter starts sign-in. The page can't be left on ChatGPT unsigned.
  - **`runSetupUI`** sets `login = chatgptauth.NewAuthClient()`, `authPath = DefaultPath()` and `chatgptStatus = ReadStatus(authPath)`. If `ReadStatus` fails, the status line reads `sign-in file unreadable: <err>`.

- [ ] **Step 1: Write the failing tests.** These use a fake loginer in the test file, an `authPath` under `t.TempDir()`, and tea.Cmd funcs run directly.
  - `TestSetupOpenAIPageStartsOnAuthRow`, `TestSetupAuthChoiceDefaultsFromSwitch` (`existing[EnvVar]="chatgpt"` → ChatGPT chosen), `TestSetupAuthToggle` (space flips the choice; API key shows the key and URL fields).
  - `TestSetupChatGPTSigninHappyPath`: enter → the code view contains the URL, `ABCD-EFGH` and both notice lines → after the done message, the token file exists at `authPath` with mode 0600, the status shows `plus plan`, and `collectValues` has `EnvVar="chatgpt"` and no `OPENAI_API_KEY`.
  - `TestSetupChatGPTSigninEscCancelsPolling` (Review Focus 4): esc while waiting → the fake `Wait` sees `ctx.Done()` within 1 s (witness channel); no token file; back on the OpenAI page; `cancelled == false`.
  - `TestSetupChatGPTSigninFailureShown`: `Start` returns `ErrDeviceCodeDisabled` → the view contains `not enabled`.
  - `TestSetupSignedInEnterContinues`, `TestSetupSignedInSResigns`.
  - `TestSetupSwitchBackToAPIKeyWritesAPIKey`: existing `chatgpt` plus a pick of API key → `values[EnvVar] == "api-key"`; existing unset → no `EnvVar` key.
  - `TestRunSetupCommandWritesSwitch`: `runSetupCommand` with a stub `runUI` returning `values{EnvVar: "chatgpt"}` → the config `.env` contains `TRACKER_OPENAI_AUTH=chatgpt`.
- [ ] **Step 2: Run** `go test ./cmd/tracker/ -run 'TestSetup|TestRunSetup' -count=1` — expect FAIL.
- [ ] **Step 3: Implement**, keeping the existing setup tests green.
- [ ] **Step 4: Docs.** In the README setup section, add a short "OpenAI on your ChatGPT subscription" subsection: what setup does, the device code, the token file location, and that it's for your own account on your own machine. In cli.html's `tracker setup` block, mention the Auth choice and device-code sign-in (badge `v0.80.0`).
- [ ] **Step 5: Run** `go test ./cmd/tracker/ -count=1 -race -short` and `make complexity` — expect PASS.
- [ ] **Step 6: Commit** `feat(setup): sign in to a ChatGPT subscription for openai`.

### Task 9: Checkpoint — first real-backend calls (needs Doctor Biz)

**Files:**
- Create: `llm/openai/chatgpt_e2e_test.go`

**Interfaces:**
- Consumes: Tasks 5, 7 and 8.

- [ ] **Step 1: Write `TestChatGPTSubscriptionLive`.**
  - It skips unless `TRACKER_E2E_CHATGPT=1` and `TRACKER_E2E_CHATGPT_MODEL` are both set; the skip message names both variables and says the test uses your real ChatGPT sign-in.
  - It builds `chatgptauth.NewTokenSource(DefaultPath(), NewAuthClient())` and `openai.New("", openai.WithChatGPTAuth(ts, chatgptauth.UserAgent()))`.
  - It sends one `Complete` and one `Stream` of `Reply with exactly: pong`, and asserts each reply contains `pong` (case-insensitive) with `Usage.InputTokens > 0`.
- [ ] **Step 2: Hand off to Doctor Biz** (the physical step): run `go run ./cmd/tracker setup`, choose OpenAI → ChatGPT subscription, finish the device code on a phone, and name a model your plan serves.
- [ ] **Step 3: Run** `TRACKER_E2E_CHATGPT=1 TRACKER_E2E_CHATGPT_MODEL=<model> go test ./llm/openai/ -run TestChatGPTSubscriptionLive -count=1 -v`.
- [ ] **Step 4: Settle spec open items 1–3** from the result. Record each in the spec's Open items, with the status and body as evidence and tokens redacted.
  1. Is `originator: tracker` accepted? If the backend refuses it with a 400 or 403 naming the client: **STOP**, update Now, and bring it to Doctor Biz. Do not switch to Codex's originator.
  2. Is `instructions` accepted? If not, change `reshapeForChatGPT` to move it into a leading `{"role":"developer","content":…}` input item (spec §2), add a unit test, and rerun.
  3. Send one request with a model the plan doesn't serve (e.g. `gpt-4.1-mini`) and record the status and body. Make `chatgptStatusError` keep that case non-retryable with the server's message, and add a unit test using the recorded shape.

  Open item 4 (one `session-id` per adapter, or one per session) stays per adapter unless this run shows a problem; note the result. Items 5–7 are settled by Tasks 5, 7 and 10.
- [ ] **Step 5: Commit** `test(openai): live ChatGPT-subscription smoke test (opt-in)`, plus any fix commits.

### Task 10: Client wiring and the fixed destination

**Files:**
- Modify: `tracker_client.go` (`buildClient`, plus the new `newChatGPTOpenAIAdapter`), `agent/session.go` (`SessionResult.Provider` at ~line 184), `cmd/tracker/doctor.go` (`formatLLMClientError`, ~line 47), `docs/architecture/backends.md` (Comparison "Auth" row, plus a short ChatGPT subsection under native), `CLAUDE.md` (Agent backends: one bullet)
- Create: `tracker_chatgpt_client_test.go`, `agent/session_usage_label_test.go`
- Test: `cmd/tracker/doctor_test.go`

**Interfaces:**
- Consumes: Tasks 2, 3, 5, 6 and 7.
- Produces:
  ```go
  var chatgptBackendURL = openai.ChatGPTBaseURL // tests point it at httptest
  func newChatGPTOpenAIAdapter() (llm.ProviderAdapter, error) // DefaultPath + NewTokenSource(path, NewAuthClient()) + openai.New("", WithChatGPTAuth(ts, chatgptauth.UserAgent()), WithBaseURL(chatgptBackendURL))
  ```
  - **`buildClient`**: call `chatgptauth.ModeFromEnv()` and return its error. In `chatgpt` mode, drop `openai` from the constructors, build the extra adapter, and call `llm.NewClientFromEnvWith(constructors, extra)`. A missing token file isn't an error here; the adapter's first call fails with `ErrNotSignedIn`. With `provider == "openai"`, register only the extra; with any other provider, no extra. Never resolve base URL or gateway settings for openai in this mode.
  - **The agent session** sets `Provider: s.usageLabel()`. That type-asserts `s.client` to `interface{ UsageLabel(string) string }` and returns `UsageLabel(s.config.Provider)`, falling back to `s.config.Provider`.
    - Before changing it, grep every read of `SessionResult.Provider` and `SessionStats.Provider`, confirm each is display or accounting rather than routing, and list them in the commit message.
  - **`formatLLMClientError`**: the "no providers configured" help adds `or sign in with your ChatGPT subscription: tracker setup → OpenAI → ChatGPT subscription`.

- [ ] **Step 1: Write the failing tests**
  - `TestBuildClientChatGPTModeRegistersOpenAIWithoutKey`: a temp `XDG_CONFIG_HOME` holding a valid token file (`fakeJWT` with `exp` now+1h), `TRACKER_OPENAI_AUTH=chatgpt`, no keys → builds; `DefaultProvider() == "openai"`; a `Complete` reaches the good httptest server (via `chatgptBackendURL`) with the file's bearer.
  - `TestBuildClientChatGPTModeIgnoresBaseURLAndGateway` (security invariant): as above, plus `OPENAI_BASE_URL=trap.URL`, `TRACKER_GATEWAY_URL=trap.URL` and `OPENAI_API_KEY=sk-test` → the request reaches the good server; the trap's request count is 0; the good server never sees `sk-test`.
  - `TestBuildClientChatGPTNotSignedInFailsOnUse`: no token file → builds; `Complete` returns a `*llm.ConfigurationError` containing `tracker setup`; an anthropic-only build is unaffected.
  - `TestBuildClientInvalidSwitch`: `TRACKER_OPENAI_AUTH=yes` → an error naming `chatgpt` and `api-key`.
  - `TestBuildClientAPIKeyModeUnchanged`: switch unset plus `OPENAI_API_KEY` → the openai adapter's `UsageLabel()` is `openai`.
  - `TestSessionResultCarriesUsageLabel`: a Completer stub whose `UsageLabel("openai")` is `openai-subscription` → `result.Provider == "openai-subscription"`; a stub without the method → `openai`.
  - `TestFormatLLMClientErrorMentionsChatGPT`.
- [ ] **Step 2: Run** `go test . ./agent/ ./cmd/tracker/ -run 'ChatGPT|UsageLabel|InvalidSwitch|APIKeyModeUnchanged' -count=1` — expect FAIL.
- [ ] **Step 3: Implement**, then update the backends.md and CLAUDE.md docs.
- [ ] **Step 4: Run** `go test . ./agent/ ./cmd/tracker/ -count=1 -short -race` and `go test ./cmd/tracker-conformance -run TestGoldenTraces -count=1` — expect PASS.
- [ ] **Step 5: Commit** `feat: route openai through the ChatGPT login when TRACKER_OPENAI_AUTH=chatgpt`.

### Task 11: Cost ceilings and estimates under the subscription

**Files:**
- Modify: `tracker.go` (`buildEngineOpts`, after `ResolveBudgetLimits` at ~line 564), `tracker_estimate.go` (`RunEstimate`, `estimateFromReport`, `turnCost`), `cmd/tracker/estimate.go` (`printEstimate`; load the config `.env` with `loadConfigEnvOnly(cfg.envFiles)` as `tracker version` does), `site/content/cli.html` (the "No other subcommand loads either `.env` file" sentence, ~line 431; the `tracker estimate` entry)
- Create: `tracker_chatgpt_budget_test.go`
- Test: `tracker_estimate_test.go`, `cmd/tracker/estimate_test.go`

**Interfaces:**
- Consumes: `chatgptauth.ModeFromEnv`, `llm.OpenAISubscription`, `llm.EstimateCostForProvider`.
- Produces:
  ```go
  // RunEstimate gains:
  SubscriptionNodes int `json:"subscription_nodes"` // openai agent nodes billed to the ChatGPT subscription ($0 here)
  func warnSubscriptionCeilings(budget pipeline.BudgetLimits, graph *pipeline.Graph) // tracker.go
  ```
  - **`warnSubscriptionCeilings`** emits one `diag.Warnf` when three things hold: the mode is `chatgpt`; at least one codergen node's provider (node `llm_provider`, else graph `llm_provider`) is `openai`; and some dollar ceiling is set (`budget.MaxCostCents > 0`, graph attr `max_cost_usd > 0`, or any node attr `max_cost_usd > 0`). Exact text: `[tracker] openai runs on your ChatGPT subscription and costs $0 here: --max-cost, max_cost_cents and max_cost_usd ceilings don't count it. --max-tokens still applies.`
  - **Estimate**: `turnCost` gains a provider parameter. An `openai` agent node in `chatgpt` mode prices via `EstimateCostForProvider(llm.OpenAISubscription, …)`, which gives 0, and counts toward `SubscriptionNodes`.
  - **`printEstimate`**: after the Cost line, when `SubscriptionNodes > 0`, print `  Subscription: %d openai node(s) on your ChatGPT subscription — $0 here; dollar ceilings don't count them.`

- [ ] **Step 1: Write the failing tests**
  - `TestWarnSubscriptionCeilings` (table; capture with `diag.SetLogger` on a buffer): chatgpt + openai node + `MaxCostCents` 400 → warns; a node-level `max_cost_usd` like build_product's ReviewCodex → warns; api-key mode → silent; no openai nodes → silent; no ceilings → silent.
  - `TestEstimateSubscriptionNodesFree`: an openai + anthropic graph in chatgpt mode → `SubscriptionNodes == 1`, openai contributes $0, anthropic unchanged; in api-key mode → `SubscriptionNodes == 0` and openai priced.
  - `TestPrintEstimateSubscriptionLine`.
  - `TestEstimateLoadsConfigEnvOnly`: a config `.env` setting `TRACKER_OPENAI_AUTH=chatgpt` and a project `.env` setting `ANTHROPIC_API_KEY` → after estimate's env load, the switch is visible and the project key isn't.
- [ ] **Step 2: Run** `go test . ./cmd/tracker/ -run 'Subscription|Estimate' -count=1` — expect FAIL.
- [ ] **Step 3: Implement**, then fix the cli.html sentence to say `tracker estimate` reads the config `.env` only.
- [ ] **Step 4: Run** the same command — expect PASS.
- [ ] **Step 5: Commit** `feat: warn that dollar ceilings skip ChatGPT-subscription calls; estimate prices them at $0`.

### Task 12: Doctor and version

**Files:**
- Create: `tracker_doctor_chatgpt.go`, `tracker_doctor_chatgpt_test.go`
- Modify: `tracker_doctor_providers.go` (`evaluateProvider`), `cmd/tracker/commands.go` (`printProviderStatus`), `site/content/cli.html` (`tracker doctor` entry, ~line 165)
- Test: `cmd/tracker/main_test.go` (or the existing version test file)

**Interfaces:**
- Consumes: chatgptauth `ModeFromEnv`, `DefaultPath`, `ReadStatus`, `Load`, `NewTokenSource`, `NewAuthClient`, `UnsafeFileError`, `ErrNotSignedIn`, `RefreshError`.
- Produces: `func evaluateChatGPT(ctx context.Context, probe bool) (CheckDetail, providerState)`, `func chatgptWarnings() []CheckDetail`, and `var doctorChatGPTIssuer = chatgptauth.Issuer` (tests point it at the fake auth server).
  - In `evaluateProvider`: `p.name == "OpenAI"` in chatgpt mode → `evaluateChatGPT`. An invalid switch → `stateInvalid` with `OpenAI          TRACKER_OPENAI_AUTH=<value> is not chatgpt or api-key`.
  - Messages use the same `%-15s` provider column as the others:
    - OK: `OpenAI          ChatGPT subscription (plus plan), signed in`, plus `, refresh ok` after a good probe. With no plan: `ChatGPT subscription, signed in`.
    - Not signed in: Error `OpenAI          TRACKER_OPENAI_AUTH=chatgpt but not signed in`, Hint `run tracker setup → OpenAI → ChatGPT subscription`.
    - Probe hits a permanent refresh failure: Error with the `RefreshError` text and the same hint.
    - Unsafe file: Error with the `UnsafeFileError` text, Hint `chmod 600 <path>`.
  - The probe is `ts.ForceRefresh(ctx, <current access token>)`: no generation request, no model.
  - Warnings in chatgpt mode (`CheckStatusWarn` details):
    - `OPENAI_BASE_URL is set but unused: openai goes to chatgpt.com in ChatGPT mode`, and the same shape for `TRACKER_GATEWAY_URL`;
    - `OPENAI_API_KEY is set but unused for openai in ChatGPT mode`;
    - always: `subscription calls cost $0 here; dollar ceilings don't count them`.
  - `printProviderStatus`: in chatgpt mode with `ReadStatus().SignedIn`, list `openai (ChatGPT subscription)` in place of `openai`.

- [ ] **Step 1: Write the failing tests:** `TestDoctorChatGPTSignedIn`, `TestDoctorChatGPTNotSignedIn`, `TestDoctorChatGPTProbeRevoked` (the fake answers 400 `refresh_token_invalidated` → Error containing `tracker setup`), `TestDoctorChatGPTUnsafeFile`, `TestDoctorChatGPTWarnings` (base URL, key and gateway set), `TestDoctorChatGPTNeverPrintsTokensOrAccount` (no detail text contains a token string or `acct-1`), `TestVersionListsChatGPTSubscription`.
- [ ] **Step 2: Run** `go test . ./cmd/tracker/ -run 'DoctorChatGPT|VersionListsChatGPT' -count=1` — expect FAIL.
- [ ] **Step 3: Implement**, then update cli.html's doctor entry with one sentence on the ChatGPT check.
- [ ] **Step 4: Run** `go test . ./cmd/tracker/ -count=1 -short` — expect PASS.
- [ ] **Step 5: Commit** `feat(doctor): report the ChatGPT subscription sign-in`.

### Task 13: Changelog, gotchas, end-to-end run, canonical check

**Files:**
- Modify: `CHANGELOG.md` (`[Unreleased]` → Added), `gotchas.md` (new section), `docs/superpowers/plans/2026-10-15-chatgpt-subscription.md` (Now)

- [ ] **Step 1: CHANGELOG** entry under Added. Cover:
  - the setup sign-in and the switch;
  - the `openai-subscription` label at $0;
  - the usage-limit pause;
  - the personal-use note;
  - the dollar-ceiling caveat (build_product's ReviewCodex cap included);
  - the autopilot-model caveat (its openai default, `gpt-4.1-mini`, may not be on the plan, so set the model explicitly).
- [ ] **Step 2: gotchas.md** gets a section `## ChatGPT subscription (Codex OAuth)` with dated bullets (`- 2026-10-15: …`):
  - Refresh tokens work once. Never share `chatgpt-auth.json` with the Codex CLI, or copy it to another machine that runs at the same time.
  - Sign-in is device code only.
  - It's for personal use; OpenAI's stated policy is quoted in the research doc, so link it.
  - Dollar ceilings skip subscription calls.
  - The backend has no non-streaming mode.
- [ ] **Step 3: End-to-end run**, using the sign-in from Task 9. Write `.scratch/chatgpt_smoke.dip` (gitignored; not committed) with one agent node (`provider: openai`, the model Doctor Biz named) and one tool node that echoes `done`. Then:
  1. `go build -o .scratch/tracker ./cmd/tracker`
  2. `.scratch/tracker doctor` → the OpenAI line is OK with `refresh ok`.
  3. `.scratch/tracker --no-tui .scratch/chatgpt_smoke.dip` → the run succeeds, and the summary's Tokens by Provider shows `openai-subscription` with tokens > 0 at $0.00.
  4. `TRACKER_E2E_CHATGPT=1 TRACKER_E2E_CHATGPT_MODEL=<model> go test ./llm/openai/ -run TestChatGPTSubscriptionLive -count=1` passes.

  Save the outputs, tokens redacted, for the PR body.
- [ ] **Step 4: Run** `make ci` — expect every gate to PASS: fmt-check, vet, build, test-short, test-race, coverage, lint, dippin-test, doctor, complexity, docs-check, shell-check, tools-jail-check.
- [ ] **Step 5: Commit** `docs: changelog and gotchas for ChatGPT-subscription auth`.
- [ ] **Step 6: Update the Now section, then ask Doctor Biz** before pushing the branch and opening the PR. The PR body has `## Problem` / `## Fix` / `## Verification` and the footer. No issue exists for this work, so leave out `Closes` / `Part of` unless Doctor Biz files one.

Tasks run in order; each one relies on the interfaces of those before it. Task 9 needs Doctor Biz at the keyboard and a phone.

## Rulings made during execution

Decisions the controller took on Doctor Biz's behalf while running this plan (subagent-driven, 2026-10-15/16). Each line: the ruling, then what it costs if wrong.

- R1: worked in the main checkout on `feat/chatgpt-subscription`, no worktree (Doctor Biz's no-worktree rule; implementers ran one at a time). Costs nothing.
- R3 (T4): device-poll tests shorten waits by setting the exported `dc.Interval`. A reviewer could prefer an injected clock; small rework.
- R4 (T8): with OpenAI unchecked in setup, `TRACKER_OPENAI_AUTH` is left untouched. To turn the subscription off, pick API key on the OpenAI page.
- R5 (T8): a saved `chatgpt` switch marks the OpenAI row configured and pre-selected. Cosmetic.
- R6 (T11): `RunEstimate.SubscriptionNodes` is a deliberate new root-package export (API golden updated). Rename or drop before merge if unwanted.
- R7 (T10, T12): package-level test seams `chatgptBackendURL` and `doctorChatGPTIssuer` (an env var would break the fixed-destination rule). Small rework if injection is preferred.
- R8/R8a/R8b: progress dashboard at `.dashboard/` (gitignored), updated at milestones only after per-task updates proved costly. The page shows stale between milestones.
- R9 (T3): `UnsafeFileError`'s fix hint depends on the problem (chmod 600 for loose modes; replace the symlink for a symlink). Wording only.
- R10 (T3): `Save` never chmods an existing config dir; the token file is always 0600. A pre-existing 0755 dir stays 0755.
- R11 → R11a (T7): a credentials error passes through as cancellation only when the caller's ctx is done; otherwise temporary → retryable `NetworkError`, else fatal `ConfigurationError`. One mapping branch.
- R12 (T8): sign-in saves through `chatgptauth.SaveSignIn`, under the same file lock as refresh. A small exported helper.
- R13 (T10): compile-time assertion that `*chatgptauth.TokenSource` satisfies `openai.ChatGPTCredentials`; one token source per token path per process. A few lines.
- R14 (T8): the spec's approved copy ("refreshed 2h ago") won over the plan's "via formatElapsed"; a small `refreshAge` helper, `formatElapsed` untouched. A ~10-line helper.
- R15 (T9): the live checkpoint ran against a scratch `XDG_CONFIG_HOME`; Doctor Biz's real `~/.config/tracker` was never touched. He signs in again for real use.
- R16 (final): doctor's ChatGPT-mode cost note and "doesn't apply to openai" lines are hints, not warnings, so a correct setup can exit 0. A user may overlook an informational note.
- R17 (final): trackerbot refuses to start with `TRACKER_OPENAI_AUTH=chatgpt` (D3: not for a shared Slack bot); trackerchat unchanged. Revert ~15 lines for a single-user Slack bot.
- Parked (final): the doctor probe's "network" hint also covers non-permanent server answers (403/429); the doctor worst case on a hung auth server is ~45 s; the setup help line doesn't mention the Auth row above the key field; spec line ~324's "every doctor error says tracker setup" wording has drifted. All shipped as is.
