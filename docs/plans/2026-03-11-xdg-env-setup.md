# Tracker XDG Env Setup Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add XDG-backed provider key loading and a Bubble Tea `tracker setup` command that writes `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, and `GEMINI_API_KEY` into the user config `.env`.

**Architecture:** Keep the change local to `cmd/tracker`: introduce a small env-config helper layer for path resolution, env loading, and `.env` file merging; route a new `setup` subcommand before the existing run path; implement the setup UX as a dedicated Bubble Tea model that gathers masked inputs and saves through the helper layer. Load order remains XDG first, then project `.env`, so local project config overrides machine defaults.

**Tech Stack:** Go, flag package, Bubble Tea, bubbles/textinput, godotenv, Go test

---

### Task 1: Add failing tests for CLI mode routing and env precedence

**Files:**
- Modify: `cmd/tracker/main_test.go`
- Modify: `cmd/tracker/main.go`

**Step 1: Write the failing tests**

Add tests that cover:

- `parseFlags([]string{"tracker", "setup"})` routes to setup mode without requiring a DOT file.
- `parseFlags([]string{"tracker", "pipeline.dot"})` still routes to run mode.
- A dedicated env-loader helper loads XDG config values first and local `.env` values second, with local values winning.
- Existing process env values are not overwritten by either file.

Use `t.TempDir()`, temporary env files, and `t.Setenv` to keep the tests isolated.

**Step 2: Run the focused test command and verify RED**

Run: `GOCACHE=$(pwd)/.gocache go test ./cmd/tracker -run 'TestParseFlags|TestLoadEnv' -count=1`

Expected: FAIL because setup mode and env loading helpers do not exist yet.

**Step 3: Implement minimal CLI mode and env loader code**

- Extend the CLI parse result to distinguish run mode from setup mode.
- Add a helper that resolves `os.UserConfigDir()/tracker/.env` and loads it before the local `.env`.
- Keep existing shell environment values intact by using non-overwriting load behavior.

**Step 4: Run the focused test command and verify GREEN**

Run: `GOCACHE=$(pwd)/.gocache go test ./cmd/tracker -run 'TestParseFlags|TestLoadEnv' -count=1`

Expected: PASS.

**Step 5: Commit**

```bash
git add cmd/tracker/main.go cmd/tracker/main_test.go
git commit -m "feat(cli): add setup mode and xdg env loading"
```

### Task 2: Add failing tests for XDG `.env` file read/write behavior

**Files:**
- Create: `cmd/tracker/config_env_test.go`
- Create: `cmd/tracker/config_env.go`

**Step 1: Write the failing tests**

Add table-driven tests for helpers that:

- Resolve the config env path under a temporary `XDG_CONFIG_HOME`.
- Read existing key/value pairs from an env file.
- Merge updated provider keys while preserving unrelated entries.
- Skip overwriting configured keys when the submitted replacement is empty.
- Write a new file and create parent directories when missing.

Keep these tests at the helper level so setup UI tests do not need to hit the filesystem directly.

**Step 2: Run the focused test command and verify RED**

Run: `GOCACHE=$(pwd)/.gocache go test ./cmd/tracker -run 'TestConfigEnv' -count=1`

Expected: FAIL because the helper package file does not exist yet.

**Step 3: Implement the minimal helper layer**

- Add a resolver for the XDG env file path.
- Add read/merge/write helpers for `.env` content.
- Preserve unrelated keys from the existing file.
- Only update `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, and `GEMINI_API_KEY` when a new non-empty value is provided.
- Write with restrictive permissions and return clear errors.

**Step 4: Run the focused test command and verify GREEN**

Run: `GOCACHE=$(pwd)/.gocache go test ./cmd/tracker -run 'TestConfigEnv' -count=1`

Expected: PASS.

**Step 5: Commit**

```bash
git add cmd/tracker/config_env.go cmd/tracker/config_env_test.go
git commit -m "feat(config): add xdg env file helpers"
```

### Task 3: Add failing tests for the Bubble Tea setup model

**Files:**
- Create: `cmd/tracker/setup_model.go`
- Create: `cmd/tracker/setup_model_test.go`

**Step 1: Write the failing tests**

Add tests that cover:

- Initial model state marks preconfigured providers without exposing their key values.
- Submitting an empty field for a preconfigured provider leaves it unchanged.
- Typing a new value marks that provider for update.
- Cancel exits without save intent.
- Save action returns a command or result payload that the outer command handler can persist.

Prefer testing model state transitions and return messages rather than full terminal rendering.

**Step 2: Run the focused test command and verify RED**

Run: `GOCACHE=$(pwd)/.gocache go test ./cmd/tracker -run 'TestSetupModel' -count=1`

Expected: FAIL because the setup model does not exist yet.

**Step 3: Implement the minimal Bubble Tea model**

- Use `bubbles/textinput` with masked input for the three provider fields.
- Track which providers are already configured.
- Keep displayed values blank for existing secrets.
- Support next/previous navigation, save, and cancel.
- Return a save payload containing only the new non-empty entries.

**Step 4: Run the focused test command and verify GREEN**

Run: `GOCACHE=$(pwd)/.gocache go test ./cmd/tracker -run 'TestSetupModel' -count=1`

Expected: PASS.

**Step 5: Commit**

```bash
git add cmd/tracker/setup_model.go cmd/tracker/setup_model_test.go
git commit -m "feat(setup): add bubble tea provider key form"
```

### Task 4: Wire `tracker setup` into the real command path

**Files:**
- Modify: `cmd/tracker/main.go`
- Modify: `cmd/tracker/main_test.go`
- Test: `cmd/tracker/config_env_test.go`
- Test: `cmd/tracker/setup_model_test.go`

**Step 1: Write the failing integration tests**

Add tests that cover:

- `main`-level command routing invokes setup behavior for `tracker setup`.
- Normal `tracker pipeline.dot` execution path is unchanged.
- Setup save writes updated keys to the XDG env file.
- Cancel leaves the file untouched.

Keep the command handler testable by factoring setup execution into a function that accepts stdin/stdout dependencies or a save callback.

**Step 2: Run the focused test command and verify RED**

Run: `GOCACHE=$(pwd)/.gocache go test ./cmd/tracker -run 'TestRunSetup|TestParseFlags' -count=1`

Expected: FAIL because the setup command is not wired into the CLI yet.

**Step 3: Implement the setup command path**

- Branch on the parsed command mode before the pipeline run path.
- Launch the Bubble Tea setup program.
- On save, call the config env helper to persist updated keys.
- On cancel, exit successfully without writes.
- Update usage text to mention `tracker setup`.

**Step 4: Run the focused test command and verify GREEN**

Run: `GOCACHE=$(pwd)/.gocache go test ./cmd/tracker -run 'TestRunSetup|TestParseFlags' -count=1`

Expected: PASS.

**Step 5: Commit**

```bash
git add cmd/tracker/main.go cmd/tracker/main_test.go cmd/tracker/config_env_test.go cmd/tracker/setup_model_test.go
git commit -m "feat(cli): wire tracker setup command"
```

### Task 5: Run full verification and update docs if needed

**Files:**
- Verify: `cmd/tracker/...`
- Verify: repository-wide test suite
- Modify: help text or docs only if verification reveals omissions

**Step 1: Run package tests for the tracker command**

Run: `GOCACHE=$(pwd)/.gocache go test ./cmd/tracker -count=1`

Expected: PASS.

**Step 2: Run the full test suite**

Run: `GOCACHE=$(pwd)/.gocache go test ./... -count=1`

Expected: PASS.

**Step 3: Manually verify the setup UX**

Run: `go run ./cmd/tracker setup`

Expected: Bubble Tea setup form opens, existing configured providers are marked without showing their values, save writes the XDG env file, and cancel exits cleanly.

**Step 4: Check help output**

Run: `go run ./cmd/tracker --help`

Expected: usage mentions both `tracker setup` and `tracker <pipeline.dot> [flags]`.

**Step 5: Commit**

```bash
git add cmd/tracker
git commit -m "test: verify xdg env setup flow"
```
