# Bedrock-gateway integration — implementation plan (issue #274)

> **For agentic workers:** REQUIRED SUB-SKILL — use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Add `TRACKER_GATEWAY_KIND` so the existing `TRACKER_GATEWAY_URL` machinery can target either Cloudflare AI Gateway (today's default) or the 2389 bedrock-gateway. Surface the OpenAI→Claude masquerade caveat in `tracker doctor`. Document the setup.

**Architecture:** Single enum (`cf-aig` | `bedrock`) governs the per-provider URL suffix map inside `tracker.ResolveProviderBaseURL`. Per-provider `*_BASE_URL` env vars still win. No new adapter types, no new auth flow. Tracker is fully transparent to upstream events (AWS adds OpenAI on Bedrock; real streaming lands).

**Tech Stack:** Go 1.24+, existing `tracker.go` / `tracker_doctor.go` / `cmd/tracker` surfaces.

**Spec:** `docs/superpowers/specs/2026-06-01-issue-274-bedrock-gateway-integration-design.md` (same branch).

**Umbrella issue:** #274 (Make Bedrock Work). This plan decomposes into three tightly-scoped child issues, listed at the bottom.

---

## File Structure

### Modified files (across the three issues)

| File | Change | Issue |
|------|--------|-------|
| `tracker.go` | Add `GatewayKind` type + suffix dispatch in `ResolveProviderBaseURL`. Add `Config.GatewayKind`. | A |
| `tracker_test.go` | New tests for KIND dispatch + per-provider override precedence. | A |
| `cmd/tracker/main.go` | New `gatewayKind` field in `runConfig`. | A |
| `cmd/tracker/flags.go` (or wherever flag parsing lives) | New `--gateway-kind` flag definition. | A |
| `cmd/tracker/commands.go` | Set `TRACKER_GATEWAY_KIND` env var from `cfg.gatewayKind` before `buildLLMClient` (mirror existing `--gateway-url` pattern). | A |
| `tracker_doctor.go` | New checks: masquerade note (D4), gateway+per-provider conflict note (D5). | B |
| `tracker_doctor_test.go` | Tests for both new checks. | B |
| `CLAUDE.md` | New `### Bedrock-gateway routing (#274)` block under Architecture Gotchas. | C |
| `CHANGELOG.md` | `[Unreleased] ### Added` entry for the gateway kind. | C |
| `docs/architecture/bedrock-gateway.md` *(new)* OR `site/content/architecture.html` section | Operator setup recipe + caveats. | C |

### New files

| File | Purpose |
|------|---------|
| `docs/architecture/bedrock-gateway.md` | Setup recipe (one URL + CF AIG token + KIND=bedrock), the OpenAI→Claude caveat, the synthesized-streaming caveat, "no code change needed when real streaming / OpenAI-on-Bedrock lands." |

---

# Issue A — routing generalization

**GitHub title:** `feat: TRACKER_GATEWAY_KIND for bedrock-gateway routing (#274)`

**Scope:** ~80 LOC + tests. Net zero behavior change for anyone not setting `TRACKER_GATEWAY_KIND=bedrock`.

## Task A.1: Add `GatewayKind` type and constants

**Files:**
- Modify: `tracker.go`

- [ ] **Step 1: Locate `ResolveProviderBaseURL` and the `Config` struct.**

`grep -n "ResolveProviderBaseURL\|type Config struct" tracker.go`

Expected: `ResolveProviderBaseURL` is the existing function around the env-driven gateway routing logic. `Config` is the library API entry point.

- [ ] **Step 2: Add the type + constants near `ResolveProviderBaseURL`.**

```go
// GatewayKind selects the path convention used when TRACKER_GATEWAY_URL is
// set. The default (cf-aig) matches Cloudflare AI Gateway's per-provider
// subpath convention; bedrock targets the 2389 bedrock-gateway Worker which
// uses native SDK URL paths.
//
// See docs/superpowers/specs/2026-06-01-issue-274-bedrock-gateway-integration-design.md.
type GatewayKind string

const (
	// GatewayKindCFAIG routes via Cloudflare AI Gateway path conventions:
	// /anthropic, /openai, /google-ai-studio, /compat. Default.
	GatewayKindCFAIG GatewayKind = "cf-aig"

	// GatewayKindBedrock routes via the 2389 bedrock-gateway Worker which
	// translates SDK requests to AWS Bedrock Converse. Uses native SDK
	// URL conventions: empty suffix for Anthropic, /v1 for OpenAI and
	// Gemini. openai-compat is not supported on this gateway.
	GatewayKindBedrock GatewayKind = "bedrock"
)

// gatewaySuffix returns the per-provider URL path suffix for the given
// gateway kind. Returns ok=false when the (kind, provider) pair is
// unsupported — callers should treat this as "do not route via gateway"
// and emit an actionable error.
func gatewaySuffix(kind GatewayKind, provider string) (string, bool) {
	switch kind {
	case "", GatewayKindCFAIG:
		switch provider {
		case "anthropic":
			return "/anthropic", true
		case "openai":
			return "/openai", true
		case "gemini":
			return "/google-ai-studio", true
		case "openai-compat":
			return "/compat", true
		}
	case GatewayKindBedrock:
		switch provider {
		case "anthropic":
			return "", true // Anthropic SDK appends /v1/messages itself
		case "openai":
			return "/v1", true
		case "gemini":
			return "/v1", true
		case "openai-compat":
			return "", false // refuse: bedrock gateway has no /compat
		}
	}
	return "", false
}
```

- [ ] **Step 3: Build clean.**

```bash
go build ./...
```

Expected: clean.

- [ ] **Step 4: Commit.**

```bash
git add tracker.go
git commit -m "$(cat <<'EOF'
feat(tracker): add GatewayKind type + gatewaySuffix dispatch (#274)

Introduces the type and helper. ResolveProviderBaseURL still uses the
hard-coded cf-aig switch in this commit; Task A.2 wires gatewaySuffix
into the resolver.

Default kind is cf-aig (zero behavior change). bedrock kind targets the
2389 bedrock-gateway Worker.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

## Task A.2: Wire `gatewaySuffix` into `ResolveProviderBaseURL`

**Files:**
- Modify: `tracker.go`
- Modify: `tracker_test.go`

- [ ] **Step 1: Write failing tests.**

Add to `tracker_test.go`:

```go
func TestResolveProviderBaseURL_GatewayKindCFAIG_BackcompatDefault(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("TRACKER_GATEWAY_URL", "https://example.com")
	t.Setenv("TRACKER_GATEWAY_KIND", "") // unset = default
	got := ResolveProviderBaseURL("anthropic")
	want := "https://example.com/anthropic"
	if got != want {
		t.Errorf("anthropic with cf-aig default = %q, want %q", got, want)
	}
}

func TestResolveProviderBaseURL_GatewayKindBedrock(t *testing.T) {
	t.Setenv("TRACKER_GATEWAY_URL", "https://bedrock-gateway.example.com")
	t.Setenv("TRACKER_GATEWAY_KIND", "bedrock")
	cases := []struct {
		provider string
		want     string
	}{
		{"anthropic", "https://bedrock-gateway.example.com"},
		{"openai", "https://bedrock-gateway.example.com/v1"},
		{"gemini", "https://bedrock-gateway.example.com/v1"},
	}
	for _, c := range cases {
		t.Setenv(strings.ToUpper(c.provider)+"_BASE_URL", "")
		t.Run(c.provider, func(t *testing.T) {
			got := ResolveProviderBaseURL(c.provider)
			if got != c.want {
				t.Errorf("%s with bedrock kind = %q, want %q", c.provider, got, c.want)
			}
		})
	}
}

func TestResolveProviderBaseURL_GatewayKindBedrock_OpenAICompatRefused(t *testing.T) {
	t.Setenv("OPENAI_COMPAT_BASE_URL", "")
	t.Setenv("TRACKER_GATEWAY_URL", "https://bedrock-gateway.example.com")
	t.Setenv("TRACKER_GATEWAY_KIND", "bedrock")
	got := ResolveProviderBaseURL("openai-compat")
	if got != "" {
		t.Errorf("openai-compat under bedrock should refuse routing (return \"\"); got %q", got)
	}
}

func TestResolveProviderBaseURL_PerProviderEnvWins(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "https://surgical.example.com")
	t.Setenv("TRACKER_GATEWAY_URL", "https://gateway.example.com")
	t.Setenv("TRACKER_GATEWAY_KIND", "bedrock")
	got := ResolveProviderBaseURL("anthropic")
	want := "https://surgical.example.com"
	if got != want {
		t.Errorf("per-provider env should win over gateway+kind; got %q want %q", got, want)
	}
}

func TestResolveProviderBaseURL_UnknownKindRefusesRouting(t *testing.T) {
	// Defensive: an unknown KIND value should refuse to route rather than
	// fall through to default. Fail-closed.
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("TRACKER_GATEWAY_URL", "https://example.com")
	t.Setenv("TRACKER_GATEWAY_KIND", "future-kind-xyz")
	got := ResolveProviderBaseURL("anthropic")
	if got != "" {
		t.Errorf("unknown KIND should refuse routing; got %q", got)
	}
}
```

- [ ] **Step 2: Run, confirm failure.**

```bash
go test ./ -run "TestResolveProviderBaseURL_Gateway|TestResolveProviderBaseURL_PerProvider|TestResolveProviderBaseURL_Unknown" -v
```

Expected: FAIL — the existing resolver doesn't read `TRACKER_GATEWAY_KIND` yet.

- [ ] **Step 3: Modify `ResolveProviderBaseURL` to use `gatewaySuffix`.**

Replace the existing per-provider switch (lines that map provider → envKey + suffix) with:

```go
func ResolveProviderBaseURL(provider string) string {
	// Per-provider env var wins unconditionally.
	switch provider {
	case "anthropic":
		if v := os.Getenv("ANTHROPIC_BASE_URL"); v != "" {
			return v
		}
	case "openai":
		if v := os.Getenv("OPENAI_BASE_URL"); v != "" {
			return v
		}
	case "gemini":
		if v := os.Getenv("GEMINI_BASE_URL"); v != "" {
			return v
		}
	case "openai-compat":
		if v := os.Getenv("OPENAI_COMPAT_BASE_URL"); v != "" {
			return v
		}
	default:
		return ""
	}

	gateway := strings.TrimRight(os.Getenv("TRACKER_GATEWAY_URL"), "/")
	if gateway == "" {
		return ""
	}

	kind := GatewayKind(os.Getenv("TRACKER_GATEWAY_KIND"))
	suffix, ok := gatewaySuffix(kind, provider)
	if !ok {
		// Unknown kind or unsupported (kind, provider) pair (e.g.
		// openai-compat with bedrock). Refuse to route — fail closed so
		// the operator sees an explicit "no base URL" rather than a
		// silent 404 against the wrong path.
		return ""
	}
	return gateway + suffix
}
```

- [ ] **Step 4: Run, confirm pass.**

```bash
go test ./ -run "TestResolveProviderBaseURL" -v
```

Expected: all PASS.

- [ ] **Step 5: Run full root-package suite.**

```bash
go test ./
```

Expected: green except `TestPinnedDippinVersionMatchesGoMod` which is the existing cross-repo dev-time failure (#272 PR will resolve). Verify it's the only failure.

- [ ] **Step 6: Commit.**

```bash
git add tracker.go tracker_test.go
git commit -m "$(cat <<'EOF'
feat(tracker): ResolveProviderBaseURL dispatches on TRACKER_GATEWAY_KIND

Generalizes the gateway routing so the same TRACKER_GATEWAY_URL can
target either Cloudflare AI Gateway (default, backcompat) or the 2389
bedrock-gateway (KIND=bedrock).

Precedence unchanged: per-provider <PROVIDER>_BASE_URL wins
unconditionally; TRACKER_GATEWAY_URL+KIND is the fallback; empty is
last resort.

Unknown KIND values refuse to route (fail-closed) rather than silently
falling through to cf-aig. openai-compat under bedrock also refuses —
the bedrock gateway has no /compat path equivalent.

Refs #274.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

## Task A.3: Add `Config.GatewayKind` library option

**Files:**
- Modify: `tracker.go`

- [ ] **Step 1: Locate `Config` struct.**

Already grepped in Task A.1. Add the `GatewayKind` field next to the existing `GatewayURL` field.

- [ ] **Step 2: Add the field.**

```go
type Config struct {
	// ... existing fields ...

	// GatewayURL, when non-empty, is exported as TRACKER_GATEWAY_URL before
	// the LLM client is built. Per-provider <PROVIDER>_BASE_URL env vars
	// still win.
	GatewayURL string

	// GatewayKind selects the path convention for TRACKER_GATEWAY_URL.
	// Empty or "cf-aig" selects Cloudflare AI Gateway conventions
	// (default). "bedrock" targets the 2389 bedrock-gateway Worker.
	// See ResolveProviderBaseURL.
	GatewayKind GatewayKind
}
```

- [ ] **Step 3: Find where `Config.GatewayURL` is applied to the env, mirror for `GatewayKind`.**

```bash
grep -n 'Setenv.*GATEWAY_URL\|Config.GatewayURL' tracker.go cmd/tracker/*.go
```

Wherever `os.Setenv("TRACKER_GATEWAY_URL", cfg.GatewayURL)` lives, add immediately after:

```go
if cfg.GatewayKind != "" {
	if err := os.Setenv("TRACKER_GATEWAY_KIND", string(cfg.GatewayKind)); err != nil {
		return fmt.Errorf("set TRACKER_GATEWAY_KIND: %w", err)
	}
}
```

- [ ] **Step 4: Build clean + run root tests.**

```bash
go build ./...
go test ./
```

Expected: green (same caveats as Task A.2).

- [ ] **Step 5: Commit.**

```bash
git add tracker.go
git commit -m "$(cat <<'EOF'
feat(tracker): Config.GatewayKind library option for #274

Library API mirror of the existing Config.GatewayURL — embedded
integrations can now toggle the bedrock-gateway path convention
without setting TRACKER_GATEWAY_KIND directly. Empty value preserves
existing cf-aig behavior.

Refs #274.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

## Task A.4: Add `--gateway-kind` CLI flag

**Files:**
- Modify: `cmd/tracker/main.go` (add `gatewayKind string` to `runConfig`)
- Modify: `cmd/tracker/<flag-parser>` (whichever file defines `--gateway-url`)
- Modify: `cmd/tracker/commands.go` (set the env var alongside the existing `--gateway-url` block)

- [ ] **Step 1: Locate the `--gateway-url` flag definition.**

```bash
grep -n "gateway-url\|gatewayURL" cmd/tracker/*.go
```

Find the file that registers the existing `--gateway-url` flag. Add `--gateway-kind` as a sibling.

- [ ] **Step 2: Add `gatewayKind` to `runConfig`.**

In `cmd/tracker/main.go`'s `runConfig` struct, immediately after the existing `gatewayURL` field:

```go
gatewayKind       string        // TRACKER_GATEWAY_KIND override — selects cf-aig (default) or bedrock path convention
```

- [ ] **Step 3: Add the flag registration.**

```go
flag.StringVar(&cfg.gatewayKind, "gateway-kind", "", "Gateway path convention: empty/cf-aig (default) or bedrock")
```

(Adapt to whatever flag library is used — likely stdlib `flag`.)

- [ ] **Step 4: Apply the flag in `cmd/tracker/commands.go`.**

Mirror the existing `--gateway-url` block (around line 302–306):

```go
if cfg.gatewayKind != "" {
	if err := os.Setenv("TRACKER_GATEWAY_KIND", cfg.gatewayKind); err != nil {
		return fmt.Errorf("set TRACKER_GATEWAY_KIND: %w", err)
	}
}
```

- [ ] **Step 5: Build clean + run cmd/tracker tests.**

```bash
go build ./...
go test ./cmd/tracker -short
```

Expected: green.

- [ ] **Step 6: Manual smoke.**

```bash
go run ./cmd/tracker --help 2>&1 | grep -A1 -i "gateway"
```

Expected: `--gateway-kind` appears in the help output with its description.

- [ ] **Step 7: Commit.**

```bash
git add cmd/tracker/main.go cmd/tracker/<flag-file>.go cmd/tracker/commands.go
git commit -m "$(cat <<'EOF'
feat(cmd): --gateway-kind CLI flag for bedrock-gateway routing

Pairs with the existing --gateway-url. Mirror of TRACKER_GATEWAY_KIND
env var; flag wins over env, env wins over default (cf-aig).

Refs #274.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

# Issue B — doctor preflight notes

**GitHub title:** `feat(doctor): surface bedrock-gateway routing caveats (#274)`

**Depends on Issue A** (reads `TRACKER_GATEWAY_KIND`).

**Scope:** ~40 LOC + tests.

## Task B.1: Add OpenAI→Claude masquerade note

**Files:**
- Modify: `tracker_doctor.go`
- Modify: `tracker_doctor_test.go`

- [ ] **Step 1: Locate the existing doctor check structure.**

```bash
grep -n "func.*Doctor\|type Doctor\|DoctorResult\|Suggestion" tracker_doctor.go
```

Find the existing per-check pattern — likely a slice of `DoctorCheck` or similar that runs and accumulates `Suggestion`/`Note` entries.

- [ ] **Step 2: Write failing test first.**

```go
func TestDoctor_BedrockOpenAIMasqueradeNote(t *testing.T) {
	t.Setenv("TRACKER_GATEWAY_URL", "https://bedrock-gateway.example.com")
	t.Setenv("TRACKER_GATEWAY_KIND", "bedrock")
	t.Setenv("OPENAI_API_KEY", "test-cf-aig-token")

	// Workflow uses provider: openai with gpt-* model — the bedrock
	// gateway today routes this to Claude Sonnet 4.6.
	out, err := Doctor(context.Background(), Config{
		// minimal config that triggers the openai-provider check;
		// adapt to whatever Doctor's actual signature requires
	})
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}

	if !strings.Contains(out.String(), "routes to Claude Sonnet 4.6") {
		t.Errorf("doctor output missing masquerade note. Got:\n%s", out)
	}
}
```

Note: the test stub above is approximate — adapt to whatever Doctor's actual return shape is. The point is: when `KIND=bedrock` AND OpenAI is configured, a specific string about the Claude masquerade should appear in the output.

- [ ] **Step 3: Run, confirm failure.**

```bash
go test ./ -run TestDoctor_BedrockOpenAIMasquerade -v
```

Expected: FAIL — note not yet emitted.

- [ ] **Step 4: Add the check.**

In `tracker_doctor.go`, add a new check function that fires only when both conditions hold:

```go
// checkBedrockOpenAIMasquerade notes that the bedrock-gateway today
// silently routes OpenAI model strings (gpt-*, o*-) to Claude Sonnet
// 4.6 because AWS hasn't added OpenAI to Bedrock yet. When that lands,
// the gateway updates its mapping; tracker needs no changes.
//
// Fires when TRACKER_GATEWAY_KIND=bedrock AND OPENAI_API_KEY is set.
// The note is informational, not an error — operators may know about
// the masquerade and accept it.
//
// See spec D4.
func checkBedrockOpenAIMasquerade() *Suggestion {
	if GatewayKind(os.Getenv("TRACKER_GATEWAY_KIND")) != GatewayKindBedrock {
		return nil
	}
	if os.Getenv("OPENAI_API_KEY") == "" {
		return nil
	}
	return &Suggestion{
		Kind: SuggestionInfo, // or whatever the existing "informational" enum is
		Message: "TRACKER_GATEWAY_KIND=bedrock: gpt-* / o*-* model strings route to Claude Sonnet 4.6 today (the bedrock gateway translates because AWS hasn't added OpenAI to Bedrock yet). When AWS adds it, the gateway updates its mapping without tracker changes. See issue #274.",
	}
}
```

Register the check in the doctor's run list (adapt to the existing pattern).

- [ ] **Step 5: Run, confirm pass.**

```bash
go test ./ -run TestDoctor_BedrockOpenAIMasquerade -v
```

Expected: PASS.

- [ ] **Step 6: Commit.**

```bash
git add tracker_doctor.go tracker_doctor_test.go
git commit -m "$(cat <<'EOF'
feat(doctor): note OpenAI→Claude masquerade under bedrock gateway

When TRACKER_GATEWAY_KIND=bedrock and OPENAI_API_KEY is set, doctor
emits a clear informational note that gpt-*/o*-* model strings route
to Claude Sonnet 4.6 today (the bedrock gateway translates because
AWS hasn't added OpenAI to Bedrock). When AWS does add it, the gateway
updates its mapping without tracker changes.

Surfaces the highest-friction "wtf, why does my gpt-4o sound like
Claude?" risk before users file a bug.

Refs #274.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

## Task B.2: Add gateway + per-provider env var conflict note

**Files:**
- Modify: `tracker_doctor.go`
- Modify: `tracker_doctor_test.go`

- [ ] **Step 1: Write failing test.**

```go
func TestDoctor_GatewayPerProviderConflict(t *testing.T) {
	t.Setenv("TRACKER_GATEWAY_URL", "https://gateway.example.com")
	t.Setenv("ANTHROPIC_BASE_URL", "https://surgical.example.com")

	out, err := Doctor(context.Background(), Config{})
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if !strings.Contains(out.String(), "ANTHROPIC_BASE_URL") {
		t.Errorf("doctor should note ANTHROPIC_BASE_URL overrides gateway routing. Got:\n%s", out)
	}
}
```

- [ ] **Step 2: Run, confirm failure.**

```bash
go test ./ -run TestDoctor_GatewayPerProviderConflict -v
```

- [ ] **Step 3: Add the check.**

```go
// checkGatewayPerProviderOverrides flags when both TRACKER_GATEWAY_URL
// and one or more <PROVIDER>_BASE_URL are set, listing which providers
// will use the surgical override rather than the gateway. Not an error
// — just visibility so operators aren't surprised by precedence.
//
// See spec D5.
func checkGatewayPerProviderOverrides() *Suggestion {
	if os.Getenv("TRACKER_GATEWAY_URL") == "" {
		return nil
	}
	var overridden []string
	for _, p := range []struct{ provider, envKey string }{
		{"anthropic", "ANTHROPIC_BASE_URL"},
		{"openai", "OPENAI_BASE_URL"},
		{"gemini", "GEMINI_BASE_URL"},
		{"openai-compat", "OPENAI_COMPAT_BASE_URL"},
	} {
		if os.Getenv(p.envKey) != "" {
			overridden = append(overridden, p.envKey)
		}
	}
	if len(overridden) == 0 {
		return nil
	}
	return &Suggestion{
		Kind: SuggestionInfo,
		Message: fmt.Sprintf("TRACKER_GATEWAY_URL is set, and these per-provider overrides are also set and will WIN over the gateway: %s", strings.Join(overridden, ", ")),
	}
}
```

Register in the doctor run list.

- [ ] **Step 4: Run, confirm pass + full doctor suite.**

```bash
go test ./ -run TestDoctor -v
```

- [ ] **Step 5: Commit.**

```bash
git add tracker_doctor.go tracker_doctor_test.go
git commit -m "$(cat <<'EOF'
feat(doctor): note when per-provider env vars override gateway routing

When TRACKER_GATEWAY_URL is set AND any <PROVIDER>_BASE_URL is also
set, doctor lists the overrides so operators can see precedence at
a glance. Not an error — surgical overrides are a feature, but the
silent precedence has surprised users.

Refs #274.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

# Issue C — docs

**GitHub title:** `docs: bedrock-gateway routing setup + CHANGELOG (#274)`

**Independent of A and B** — can land in parallel.

**Scope:** Content only, no code.

## Task C.1: Operator setup recipe

**Files:**
- Create: `docs/architecture/bedrock-gateway.md`

- [ ] **Step 1: Write the file.**

```markdown
# Bedrock-gateway routing

Tracker routes provider SDK calls through any gateway that speaks the
provider's wire format. Two gateway kinds are supported today:

## Cloudflare AI Gateway (`cf-aig`, default)

```bash
export TRACKER_GATEWAY_URL=https://gateway.ai.cloudflare.com/v1/<acct>/<gateway-id>
# TRACKER_GATEWAY_KIND defaults to cf-aig; explicit is fine too
export ANTHROPIC_API_KEY=sk-...
export OPENAI_API_KEY=sk-...
```

Tracker appends `/anthropic`, `/openai`, `/google-ai-studio`,
`/compat` per provider.

## Bedrock gateway (`bedrock`)

The [2389 bedrock-gateway](https://github.com/2389-research/gateway) is a
Cloudflare Worker that translates SDK requests to AWS Bedrock Converse.
One Cloudflare AI Gateway token unlocks all three providers.

```bash
export TRACKER_GATEWAY_URL=https://bedrock-gateway.2389-research-inc.workers.dev
export TRACKER_GATEWAY_KIND=bedrock
export ANTHROPIC_API_KEY=<cf-aig-token>
export OPENAI_API_KEY=<cf-aig-token>
export GEMINI_API_KEY=<cf-aig-token>
```

Or via CLI flag:

```bash
tracker run my-workflow.dip \
  --gateway-url https://bedrock-gateway.2389-research-inc.workers.dev \
  --gateway-kind bedrock
```

## Caveats (bedrock kind only)

### OpenAI models route to Claude today

AWS Bedrock does not yet support OpenAI models. The bedrock gateway
maps `gpt-*` and `o*-*` model strings to Claude Sonnet 4.6
transparently. **If you set `provider: openai` and `model: gpt-4o`
in a `.dip` file today, you will get Claude responses, Claude latency,
and Claude pricing in the cost summary.**

`tracker doctor` will note this when it sees the combination.

When AWS adds OpenAI to Bedrock, the gateway updates its mapping
with no tracker changes required. Existing workflows automatically
get real OpenAI on Bedrock.

### Streaming is synthesized today

The bedrock gateway currently calls Bedrock non-streaming and emits
chunks from the completed response. Time-to-first-byte is fast
(keepalive ping) but time-to-first-token equals total generation
latency — content arrives in a burst.

[Gateway issue #14](https://github.com/2389-research/gateway/issues/14)
tracks the real-streaming overhaul. When it lands, tracker's TUI
automatically gets progressively-displayed tokens — no code changes.

### `openai-compat` is not supported

The bedrock gateway has no `/compat` equivalent. Setting
`provider: openai-compat` with `KIND=bedrock` makes tracker return
an empty base URL (the resolver fails closed), and the SDK falls
back to its default endpoint. Use `provider: openai` for OpenAI-shaped
calls through the bedrock gateway.

## Precedence

Per-provider env vars win over gateway routing:

1. `<PROVIDER>_BASE_URL` (explicit) — wins unconditionally
2. `TRACKER_GATEWAY_URL` + `TRACKER_GATEWAY_KIND` — fallback
3. Empty (SDK default endpoint) — last resort

`tracker doctor` lists active overrides when both layers are set.
```

- [ ] **Step 2: Commit.**

```bash
git add docs/architecture/bedrock-gateway.md
git commit -m "docs: bedrock-gateway routing setup (#274)"
```

## Task C.2: CLAUDE.md gotcha entry

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Locate the Architecture Gotchas section.**

Most recent entry from #272 is `### `tracker __jail-exec` internal subcommand (#272)`. Append after it.

- [ ] **Step 2: Add the section.**

```markdown
### Gateway routing kinds (#274)

`TRACKER_GATEWAY_URL` is consulted in conjunction with `TRACKER_GATEWAY_KIND`
(or `--gateway-kind`) which selects the per-provider URL suffix
convention:

- `cf-aig` (default) — Cloudflare AI Gateway: `/anthropic`, `/openai`,
  `/google-ai-studio`, `/compat`.
- `bedrock` — the 2389 bedrock-gateway Worker: empty suffix for Anthropic,
  `/v1` for OpenAI and Gemini; `openai-compat` is unsupported and the
  resolver returns empty (fail-closed).

Per-provider `<PROVIDER>_BASE_URL` env vars still win over the gateway
in both kinds. Unknown KIND values refuse to route (return empty
base URL) rather than falling through to default.

Tracker is fully transparent to upstream gateway events:
- AWS adds OpenAI on Bedrock → gateway updates its model mapping; no
  tracker code change.
- Real Bedrock streaming lands → SSE wire shape is identical; tracker
  TUI just gets progressive tokens automatically.

`tracker doctor` notes the OpenAI→Claude masquerade and any
gateway+per-provider precedence overlaps. See
`docs/architecture/bedrock-gateway.md`.
```

- [ ] **Step 3: Commit.**

```bash
git add CLAUDE.md
git commit -m "docs(claude-md): gateway routing kinds gotcha (#274)"
```

## Task C.3: CHANGELOG entry

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Add under `[Unreleased] → ### Added`.**

```markdown
- **Bedrock gateway routing** (refs #274). New `TRACKER_GATEWAY_KIND` env
  var and `--gateway-kind` CLI flag select the per-provider URL suffix
  convention used with `TRACKER_GATEWAY_URL`. Two kinds supported:
  - `cf-aig` (default, backcompat) — Cloudflare AI Gateway path conventions.
  - `bedrock` — the 2389 bedrock-gateway Worker. One CF AIG token works
    as the API key for all three providers (Anthropic, OpenAI, Gemini).

  **Operator caveats** (bedrock kind only): OpenAI model strings (gpt-*,
  o*-*) route to Claude Sonnet 4.6 today because AWS Bedrock does not yet
  support OpenAI models — the gateway will update its mapping when AWS
  does, with no tracker changes required. Streaming is currently synthesized
  (TTFT = full generation latency); real Bedrock streaming work is upstream
  in the gateway repo. `openai-compat` provider is not supported on the
  bedrock gateway.

  `tracker doctor` surfaces both caveats. See
  [docs/architecture/bedrock-gateway.md](docs/architecture/bedrock-gateway.md).
```

- [ ] **Step 2: Commit.**

```bash
git add CHANGELOG.md
git commit -m "docs(changelog): bedrock gateway routing (#274)"
```

---

## Final verification (all three issues complete)

- [ ] `go build ./...` clean on Linux + `GOOS=darwin GOARCH=amd64`.
- [ ] `go test ./... -short` green (`TestPinnedDippinVersionMatchesGoMod` may fail if #272 PR still open — known intentional dev state).
- [ ] `tracker --help` shows `--gateway-kind`.
- [ ] Manual smoke A: cf-aig kind against a real Cloudflare AI Gateway URL — back-compat unchanged.
- [ ] Manual smoke B: bedrock kind against `https://bedrock-gateway.2389-research-inc.workers.dev` with a CF AIG token + `ANTHROPIC_API_KEY` — returns a completion for `provider: anthropic, model: claude-sonnet-4-6`.
- [ ] Manual smoke C: `tracker doctor` with `KIND=bedrock` + `OPENAI_API_KEY` set — emits the masquerade note.

## Child issues to file (tightly scoped)

After spec/plan land, file three child issues against #274:

| Issue | Title | Scope | Depends on |
|-------|-------|-------|------------|
| A | `feat: TRACKER_GATEWAY_KIND for bedrock-gateway routing (#274)` | Tasks A.1–A.4 (~80 LOC + tests) | — |
| B | `feat(doctor): surface bedrock-gateway routing caveats (#274)` | Tasks B.1–B.2 (~40 LOC + tests) | A |
| C | `docs: bedrock-gateway routing setup + CHANGELOG (#274)` | Tasks C.1–C.3 (content only) | — |

Each issue references back to umbrella #274 and links to this plan.
