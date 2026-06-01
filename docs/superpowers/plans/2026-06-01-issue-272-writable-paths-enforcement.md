# writable_paths fs-jail enforcement (issue #272) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the tracker-side runtime enforcement of dippin's `writable_paths` primitive — write containment for in-process tools AND Bash subprocess + descendants — bounded to per-agent globs resolved against an immutable session root.

**Architecture:** Hybrid Landlock. Linux Landlock LSM ABI v3 (kernel 6.7+) for Bash subprocess via `tracker __jail-exec` self-re-exec; Go-level `openat2(RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS | RESOLVE_NO_MAGICLINKS)` against a session-root file descriptor for in-process Write/Edit/ApplyPatch. macOS/Windows/older Linux refuse-to-start when writable_paths is set. claude-code/acp backends also refuse-to-start.

**Tech Stack:** Go 1.24+, `github.com/landlock-lsm/go-landlock` for Landlock bindings, `golang.org/x/sys/unix` for `openat2` syscall, existing tracker IR/adapter/handler stack.

**Spec:** `docs/superpowers/specs/2026-06-01-issue-272-writable-paths-enforcement-design.md` (this branch).

**Dippin pin during dev:** `@latest` pseudo-version (dippin PR #83 merged to `main`, untagged). Release PR bumps to v0.35.0 tag and `tracker_doctor.go PinnedDippinVersion` in lockstep.

---

## File Structure

### New files

| File | Purpose |
|------|---------|
| `agent/exec/jail_linux.go` | Linux implementation: `WrapBashCmd`, `RunJailExec`, `ProbeLandlock`, `ValidateWritablePaths`, `OpenForWrite`. `//go:build linux`. |
| `agent/exec/jail_other.go` | Passthrough stubs for `!linux`. `WrapBashCmd` returns the cmd unchanged; `ProbeLandlock` returns `ErrLandlockUnavailable`; `OpenForWrite` returns `ErrLandlockUnavailable`. `ValidateWritablePaths` is shared (pure Go path math). |
| `agent/exec/jail_linux_test.go` | Unit tests for the Linux-only helpers. Uses `TestMain` re-exec idiom for `RunJailExec`. |
| `agent/exec/jail_test.go` | Cross-platform unit tests for `ValidateWritablePaths` (pure Go, no syscalls). |
| `agent/exec/jail_errors.go` | Shared error sentinels: `ErrLandlockUnavailable`, `ErrPathEscape`, `ErrPathNotAllowed`. |
| `pipeline/handlers/codergen_jail.go` | `configureJail(cfg, attrs, env) (bool, error)` helper called from `buildConfig`. Co-located with codergen handler. |
| `pipeline/handlers/codergen_jail_test.go` | Tests for the three refuse-to-start gates. |

### Modified files

| File | Change summary |
|------|---|
| `go.mod` | Bump `github.com/2389-research/dippin-lang` to `@latest` (resolves to dippin's main with PR #83). Add `github.com/landlock-lsm/go-landlock` dep. |
| `pipeline/node_config.go` | Add `WritablePaths []string` field on `AgentNodeConfig` + typed accessor. |
| `pipeline/dippin_adapter.go` | Set `attrs["writable_paths"]` from `cfg.WritablePaths` BEFORE the Params spill. |
| `agent/config.go` | Add `WritablePaths []string` field on `SessionConfig`. |
| `agent/exec/local.go` | Add optional `CommandWrapper func(*exec.Cmd) *exec.Cmd` and `WriteOpener func(string, os.FileMode) (*os.File, error)` fields on `LocalEnvironment`. `ExecCommand` and `WriteFile` consult them when non-nil. |
| `pipeline/handlers/codergen.go` | Call `configureJail` from `buildConfig`; wire env's `CommandWrapper` + `WriteOpener`. |
| `cmd/tracker/main.go` | Early `if os.Args[1] == "__jail-exec"` dispatch to `exec.RunJailExec(os.Args[2:])`. |
| `CLAUDE.md` | One paragraph in "Architecture Gotchas" describing the re-exec mechanism. |
| `site/static/skill.md` | One paragraph: tracker-side enforcement contract, two-tier semantic, residual escape classes. |
| `CHANGELOG.md` | `[Unreleased]` entry per spec § 11. |
| `pipeline/handlers/parallel_test.go` | Add `TestParallelBranchSymlinkRace`. |

---

## Chunk 1 — Dippin pin + IR plumbing

### Task 1: Bump dippin-lang to `@latest`

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Bump dippin pseudo-version**

```bash
go get github.com/2389-research/dippin-lang@latest
go mod tidy
```

- [ ] **Step 2: Verify the IR field landed**

```bash
go doc github.com/2389-research/dippin-lang/ir AgentConfig 2>&1 | grep -i writable_paths
```

Expected: a line like `WritablePaths []string ...`. If absent, dippin's `main` doesn't yet have the field merged — escalate; do not invent the field.

- [ ] **Step 3: Verify build clean**

```bash
go build ./...
```

Expected: clean (no new tracker code uses the IR field yet).

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "$(cat <<'EOF'
build(deps): bump dippin-lang to @latest for WritablePaths IR field

Pseudo-version pin to dippin's main during cross-repo joint-release dev.
Release PR replaces with the tagged dippin v0.35.0 and bumps
PinnedDippinVersion in lockstep.

Part of #272.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Add `WritablePaths` to `AgentNodeConfig` + typed accessor

**Files:**
- Modify: `pipeline/node_config.go`
- Modify: `pipeline/node_config_test.go` (or whichever existing test file covers `AgentConfig()`)

- [ ] **Step 1: Write failing test**

Find the existing tests for `AgentConfig()` (likely `TestAgentConfig_*`). Add:

```go
func TestAgentConfig_WritablePaths(t *testing.T) {
    cases := []struct {
        name  string
        attrs map[string]string
        want  []string
    }{
        {
            name:  "absent",
            attrs: map[string]string{},
            want:  nil,
        },
        {
            name:  "single glob",
            attrs: map[string]string{"writable_paths": "workspace/**"},
            want:  []string{"workspace/**"},
        },
        {
            name:  "comma-separated",
            attrs: map[string]string{"writable_paths": "workspace/**,.ai/sprints/**,.ai/managers/recovery-journal.md"},
            want:  []string{"workspace/**", ".ai/sprints/**", ".ai/managers/recovery-journal.md"},
        },
        {
            name:  "whitespace trimmed",
            attrs: map[string]string{"writable_paths": " workspace/** ,  .ai/sprints/** "},
            want:  []string{"workspace/**", ".ai/sprints/**"},
        },
        {
            name:  "empty entries dropped",
            attrs: map[string]string{"writable_paths": "workspace/**,,.ai/sprints/**"},
            want:  []string{"workspace/**", ".ai/sprints/**"},
        },
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            n := &Node{Attrs: tc.attrs}
            got := n.AgentConfig(nil).WritablePaths
            if !slices.Equal(got, tc.want) {
                t.Errorf("WritablePaths = %v, want %v", got, tc.want)
            }
        })
    }
}
```

- [ ] **Step 2: Run, confirm failure**

```bash
go test ./pipeline/ -run TestAgentConfig_WritablePaths -v
```

Expected: FAIL — `WritablePaths` field undefined.

- [ ] **Step 3: Add the field to `AgentConfig` struct + accessor**

In `pipeline/node_config.go`, find the `AgentConfig` struct (returned by `*Node.AgentConfig(graphAttrs)`). Add the field:

```go
type AgentConfig struct {
    // ... existing fields ...

    // WritablePaths bounds the file paths this agent's tools may write,
    // as author-chosen globs resolved against the session root. Empty/absent
    // = unbounded. Non-empty triggers the runtime fs-jail (Linux Landlock for
    // Bash subprocess + openat2 for in-process Write/Edit/ApplyPatch). A
    // present-but-empty or malformed value fails CLOSED at session creation
    // (deny-all / refuse-to-start), never unbounded. See issue #272.
    WritablePaths []string
}
```

In `*Node.AgentConfig(graphAttrs)` (the function that populates the struct from `n.Attrs`), add:

```go
// Just before the function returns:
if raw, ok := n.Attrs["writable_paths"]; ok && raw != "" {
    cfg.WritablePaths = splitCommaNoEmpty(raw)
}
```

If `splitCommaNoEmpty` doesn't already exist in `pipeline/node_config.go`, add it:

```go
// splitCommaNoEmpty splits s on commas, trims whitespace from each entry, and
// drops empty entries. Mirrors dippin's parser/parse_nodes.go splitCommaNoEmpty
// so the round-trip from .dip → IR → adapter → AgentConfig produces identical
// slices regardless of which path was taken.
func splitCommaNoEmpty(s string) []string {
    if s == "" {
        return nil
    }
    parts := strings.Split(s, ",")
    out := make([]string, 0, len(parts))
    for _, p := range parts {
        p = strings.TrimSpace(p)
        if p != "" {
            out = append(out, p)
        }
    }
    if len(out) == 0 {
        return nil
    }
    return out
}
```

- [ ] **Step 4: Run, confirm pass**

```bash
go test ./pipeline/ -run TestAgentConfig_WritablePaths -v
```

Expected: PASS (5 subtests).

- [ ] **Step 5: Run full pipeline suite**

```bash
go test ./pipeline/ -short
```

Expected: all green.

- [ ] **Step 6: Commit**

```bash
git add pipeline/node_config.go pipeline/node_config_test.go
git commit -m "$(cat <<'EOF'
feat(pipeline): add WritablePaths typed accessor on AgentConfig

Reads node.Attrs["writable_paths"] as a comma-separated glob list, with
empty entries dropped (matches dippin parser's splitCommaNoEmpty). Per
CLAUDE.md typed-config convention; consumers must use the accessor, not
read node.Attrs directly.

Part of #272.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Adapter sets `attrs["writable_paths"]` from typed IR field (typed-wins precedence)

**Files:**
- Modify: `pipeline/dippin_adapter.go`
- Modify: `pipeline/dippin_adapter_test.go`

- [ ] **Step 1: Write failing test**

Add to `pipeline/dippin_adapter_test.go`:

```go
func TestExtractAgentAttrs_WritablePaths(t *testing.T) {
    t.Run("typed field populates attrs", func(t *testing.T) {
        ir := &ir.AgentConfig{
            WritablePaths: []string{"workspace/**", ".ai/sprints/**"},
        }
        attrs := extractAgentAttrs(ir)
        got, ok := attrs["writable_paths"]
        if !ok {
            t.Fatal("attrs missing writable_paths key")
        }
        if got != "workspace/**,.ai/sprints/**" {
            t.Errorf("attrs[writable_paths] = %q, want %q", got, "workspace/**,.ai/sprints/**")
        }
    })

    t.Run("Params writable_paths cannot override typed field", func(t *testing.T) {
        ir := &ir.AgentConfig{
            WritablePaths: []string{"workspace/**"},
            Params:        map[string]string{"writable_paths": "/etc/**"},
        }
        attrs := extractAgentAttrs(ir)
        if attrs["writable_paths"] != "workspace/**" {
            t.Errorf("Params override won (got %q); typed field should have won (want %q)",
                attrs["writable_paths"], "workspace/**")
        }
    })

    t.Run("Params writable_paths fills in when typed empty", func(t *testing.T) {
        // Defensible behavior: if a workflow author for some reason writes
        // writable_paths under params: instead of as a first-class field,
        // tracker still picks it up via the spill (and the dippin lint flags
        // this as bad style at pack time). The defense is "typed wins";
        // there is no defense against "no typed value at all".
        ir := &ir.AgentConfig{
            WritablePaths: nil,
            Params:        map[string]string{"writable_paths": "workspace/**"},
        }
        attrs := extractAgentAttrs(ir)
        if attrs["writable_paths"] != "workspace/**" {
            t.Errorf("Params spill on empty typed field should yield %q, got %q",
                "workspace/**", attrs["writable_paths"])
        }
    })
}
```

- [ ] **Step 2: Run, confirm failure**

```bash
go test ./pipeline/ -run TestExtractAgentAttrs_WritablePaths -v
```

Expected: FAIL — `attrs["writable_paths"]` is empty (typed field not yet wired).

- [ ] **Step 3: Modify `extractAgentAttrs`**

In `pipeline/dippin_adapter.go`, find `extractAgentAttrs` (around line 277-293 per the spec). Locate the block that writes typed fields BEFORE the Params spill. Add:

```go
// (inside extractAgentAttrs, where typed fields write to attrs, BEFORE the Params spill)
if len(cfg.WritablePaths) > 0 {
    attrs["writable_paths"] = strings.Join(cfg.WritablePaths, ",")
}
```

Verify the Params spill loop still uses the `if _, exists := attrs[k]; !exists` guard (around line 289). It should — this is the typed-wins precedence the test verifies.

- [ ] **Step 4: Run, confirm pass**

```bash
go test ./pipeline/ -run TestExtractAgentAttrs_WritablePaths -v
```

Expected: PASS (3 subtests).

- [ ] **Step 5: Run adapter suite**

```bash
go test ./pipeline/ -run TestExtractAgentAttrs -v
go test ./pipeline/ -short
```

Expected: all green.

- [ ] **Step 6: Commit**

```bash
git add pipeline/dippin_adapter.go pipeline/dippin_adapter_test.go
git commit -m "$(cat <<'EOF'
feat(adapter): write writable_paths to node.Attrs from typed IR field

extractAgentAttrs sets attrs["writable_paths"] from ir.AgentConfig.WritablePaths
BEFORE the Params spill. The existing typed-wins precedence (line 289 guard
'if _, exists := attrs[k]; !exists') makes Params overrides no-ops — this is
the bypass defense for the writable_paths field per spec D8.

Part of #272.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Chunk 2 — SessionConfig + LocalEnvironment seams

### Task 4: Add `WritablePaths` to `SessionConfig`

**Files:**
- Modify: `agent/config.go`
- Modify: `agent/config_test.go`

- [ ] **Step 1: Write failing test**

Add to `agent/config_test.go`:

```go
func TestSessionConfig_WritablePaths(t *testing.T) {
    cfg := DefaultConfig()
    if cfg.WritablePaths != nil {
        t.Errorf("default WritablePaths = %v, want nil", cfg.WritablePaths)
    }

    cfg.WritablePaths = []string{"workspace/**"}
    if err := cfg.Validate(); err != nil {
        t.Errorf("Validate with WritablePaths set = %v, want nil", err)
    }
}
```

- [ ] **Step 2: Run, confirm failure**

```bash
go test ./agent/ -run TestSessionConfig_WritablePaths -v
```

Expected: FAIL — field undefined.

- [ ] **Step 3: Add the field**

In `agent/config.go`, find the `SessionConfig` struct. Add:

```go
type SessionConfig struct {
    // ... existing fields ...

    // WritablePaths is the author-declared write-scope glob list resolved
    // against WorkingDir. Empty/absent = unbounded; non-empty = jail enforced
    // by the runtime (Linux Landlock for Bash subprocess + openat2 for
    // in-process tools). Empty values, malformed globs, working_dir escapes,
    // unsupported backends, and Landlock-unavailable hosts all refuse-to-start
    // at session creation. See issue #272.
    WritablePaths []string
}
```

`Validate()` does NOT need a new check — the validation runs in `ValidateWritablePaths` at session setup, not here. Leave `Validate()` unchanged.

- [ ] **Step 4: Run, confirm pass**

```bash
go test ./agent/ -run TestSessionConfig_WritablePaths -v
go test ./agent/ -short
```

Expected: all green.

- [ ] **Step 5: Commit**

```bash
git add agent/config.go agent/config_test.go
git commit -m "$(cat <<'EOF'
feat(agent): add WritablePaths to SessionConfig

Author-declared glob list resolved against WorkingDir. The codergen handler
populates this from the typed AgentNodeConfig accessor; the agent runtime
consults it via the jail wiring in agent/exec/local.go (Task 5).

Part of #272.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: Add `CommandWrapper` and `WriteOpener` optional function fields to `LocalEnvironment`

**Files:**
- Modify: `agent/exec/local.go`
- Modify: `agent/exec/local_test.go` (or wherever `LocalEnvironment` tests live)

- [ ] **Step 1: Write failing test**

Add to the local-environment test file:

```go
func TestLocalEnvironment_CommandWrapperApplied(t *testing.T) {
    env := NewLocalEnvironment(t.TempDir())
    wrapped := false
    env.CommandWrapper = func(cmd *exec.Cmd) *exec.Cmd {
        wrapped = true
        return cmd
    }
    _, err := env.ExecCommand(context.Background(), "/bin/true", nil, 5*time.Second)
    if err != nil {
        t.Fatalf("ExecCommand: %v", err)
    }
    if !wrapped {
        t.Error("CommandWrapper was not invoked")
    }
}

func TestLocalEnvironment_WriteOpenerApplied(t *testing.T) {
    dir := t.TempDir()
    env := NewLocalEnvironment(dir)
    opened := false
    env.WriteOpener = func(path string, perm os.FileMode) (*os.File, error) {
        opened = true
        return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
    }
    err := env.WriteFile("test.txt", []byte("hello"), 0644)
    if err != nil {
        t.Fatalf("WriteFile: %v", err)
    }
    if !opened {
        t.Error("WriteOpener was not invoked")
    }
}

func TestLocalEnvironment_NoWrapperFallsThrough(t *testing.T) {
    env := NewLocalEnvironment(t.TempDir())
    // Both function fields nil — must fall through to existing behavior.
    _, err := env.ExecCommand(context.Background(), "/bin/true", nil, 5*time.Second)
    if err != nil {
        t.Errorf("ExecCommand with nil CommandWrapper = %v, want nil", err)
    }
    err = env.WriteFile("test.txt", []byte("hello"), 0644)
    if err != nil {
        t.Errorf("WriteFile with nil WriteOpener = %v, want nil", err)
    }
}
```

- [ ] **Step 2: Run, confirm failure**

```bash
go test ./agent/exec/ -run "TestLocalEnvironment_CommandWrapper|TestLocalEnvironment_WriteOpener|TestLocalEnvironment_NoWrapper" -v
```

Expected: FAIL — fields undefined.

- [ ] **Step 3: Add the fields**

In `agent/exec/local.go`, find the `LocalEnvironment` struct. Add:

```go
type LocalEnvironment struct {
    workDir string
    // ... existing fields ...

    // CommandWrapper, when non-nil, is applied to every *exec.Cmd that
    // ExecCommand constructs. The writable_paths fs-jail (issue #272) uses
    // this to rewrite Bash invocations through tracker's __jail-exec
    // self-re-exec, applying Landlock before the agent command runs.
    // Default nil — the environment behaves as before.
    CommandWrapper func(*exec.Cmd) *exec.Cmd

    // WriteOpener, when non-nil, replaces os.OpenFile in WriteFile. The
    // writable_paths fs-jail (issue #272) sets this to an openat2-backed
    // opener that enforces RESOLVE_BENEATH + RESOLVE_NO_SYMLINKS against
    // the session-root file descriptor. Default nil — WriteFile uses
    // os.WriteFile as before.
    WriteOpener func(path string, perm os.FileMode) (*os.File, error)
}
```

In `ExecCommand`, after the `cmd.Dir = e.workDir` line but BEFORE `cmd.Run()`/`cmd.Start()`:

```go
if e.CommandWrapper != nil {
    cmd = e.CommandWrapper(cmd)
}
```

In `WriteFile`, replace the current `os.WriteFile` call (or equivalent) with:

```go
func (e *LocalEnvironment) WriteFile(relPath string, data []byte, perm os.FileMode) error {
    absPath := filepath.Join(e.workDir, relPath)
    if e.WriteOpener != nil {
        f, err := e.WriteOpener(absPath, perm)
        if err != nil {
            return err
        }
        defer f.Close()
        _, err = f.Write(data)
        return err
    }
    return os.WriteFile(absPath, data, perm)
}
```

(Adapt to the actual existing `WriteFile` signature. If `LocalEnvironment` does not currently have a `WriteFile` method but tools call `os.WriteFile` directly, ADD `WriteFile` as a new method with the above shape and refactor `agent/tools/write.go`/`edit.go`/`apply_patch.go` to call `e.WriteFile` instead of `os.WriteFile`. **However**: the spec explicitly says zero changes to those tool files. Verify first: does `LocalEnvironment.WriteFile` already exist? If yes, modify in place. If no, the env may already expose a different write seam — locate it and modify there.)

- [ ] **Step 4: Run, confirm pass**

```bash
go test ./agent/exec/ -run "TestLocalEnvironment" -v
```

Expected: PASS (3 new tests + existing).

- [ ] **Step 5: Run full agent suite**

```bash
go test ./agent/... -short
```

Expected: all green (no behavior change when fields are nil).

- [ ] **Step 6: Commit**

```bash
git add agent/exec/local.go agent/exec/local_test.go
git commit -m "$(cat <<'EOF'
feat(exec): add CommandWrapper + WriteOpener seams to LocalEnvironment

Optional function fields the writable_paths fs-jail (#272) uses to inject
its enforcement without touching tool files. CommandWrapper rewrites Bash's
*exec.Cmd to re-exec through tracker __jail-exec; WriteOpener replaces
os.OpenFile with an openat2-backed implementation.

Both default nil; the environment behaves as before when unused. The jail
is configured by the codergen handler when SessionConfig.WritablePaths is
non-empty.

Part of #272.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Chunk 3 — Jail Linux foundations

### Task 6: Create `jail_errors.go` shared sentinels

**Files:**
- Create: `agent/exec/jail_errors.go`
- Create: `agent/exec/jail_errors_test.go`

- [ ] **Step 1: Write failing test**

```go
// agent/exec/jail_errors_test.go
package exec

import (
    "errors"
    "fmt"
    "testing"
)

func TestJailErrorsSentinels(t *testing.T) {
    cases := []struct {
        name string
        err  error
    }{
        {"ErrLandlockUnavailable", ErrLandlockUnavailable},
        {"ErrPathEscape", ErrPathEscape},
        {"ErrPathNotAllowed", ErrPathNotAllowed},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            if tc.err == nil {
                t.Fatal("sentinel is nil")
            }
            if tc.err.Error() == "" {
                t.Fatal("sentinel has empty message")
            }
            wrapped := fmt.Errorf("context: %w", tc.err)
            if !errors.Is(wrapped, tc.err) {
                t.Errorf("errors.Is failed for wrapped %s", tc.name)
            }
        })
    }
}
```

- [ ] **Step 2: Run, confirm failure**

```bash
go test ./agent/exec/ -run TestJailErrorsSentinels -v
```

Expected: FAIL — sentinels undefined.

- [ ] **Step 3: Create `agent/exec/jail_errors.go`**

```go
// ABOUTME: Shared error sentinels for the writable_paths fs-jail (issue #272).
// ABOUTME: Used by both Linux jail implementation and non-Linux passthrough stubs.
package exec

import "errors"

// ErrLandlockUnavailable is returned by ProbeLandlock when the host kernel
// doesn't support Landlock ABI v3 (kernel 6.7+), or when the binary is built
// for a non-Linux target. The codergen handler refuses to start a session
// with non-empty WritablePaths on this error.
var ErrLandlockUnavailable = errors.New("Landlock ABI v3 not available on this host (requires Linux kernel 6.7+)")

// ErrPathEscape is returned by OpenForWrite when the requested path resolves
// outside the session anchor (via absolute path, parent traversal, or symlink
// escape). The kernel returns EXDEV/ELOOP for openat2 with RESOLVE_BENEATH;
// the helper translates to this sentinel for typed handling upstream.
var ErrPathEscape = errors.New("write path escapes session root")

// ErrPathNotAllowed is returned by OpenForWrite when the resolved path is
// beneath the session anchor but does not match any writable_paths glob.
var ErrPathNotAllowed = errors.New("write path not in writable_paths")
```

- [ ] **Step 4: Run, confirm pass**

```bash
go test ./agent/exec/ -run TestJailErrorsSentinels -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add agent/exec/jail_errors.go agent/exec/jail_errors_test.go
git commit -m "$(cat <<'EOF'
feat(exec): add jail error sentinels

Three errors.New sentinels shared by the Linux jail implementation and the
non-Linux passthrough stubs:
- ErrLandlockUnavailable
- ErrPathEscape
- ErrPathNotAllowed

All wrap cleanly through fmt.Errorf %w for errors.Is matching.

Part of #272.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: Create `jail_other.go` passthrough stubs

**Files:**
- Create: `agent/exec/jail_other.go`
- Create: `agent/exec/jail_other_test.go`

- [ ] **Step 1: Write failing test (build-tag-gated)**

```go
//go:build !linux
// +build !linux

package exec

import (
    "context"
    "errors"
    "os/exec"
    "testing"
)

func TestProbeLandlock_NonLinux(t *testing.T) {
    err := ProbeLandlock()
    if !errors.Is(err, ErrLandlockUnavailable) {
        t.Errorf("ProbeLandlock on non-Linux = %v, want ErrLandlockUnavailable", err)
    }
}

func TestWrapBashCmd_NonLinux_Passthrough(t *testing.T) {
    cmd := exec.CommandContext(context.Background(), "/bin/echo", "hello")
    out := WrapBashCmd(cmd, "/tmp/anchor", []string{"workspace/**"})
    if out != cmd {
        t.Errorf("WrapBashCmd on non-Linux returned %p, want passthrough %p", out, cmd)
    }
    if len(out.Args) != 2 || out.Args[1] != "hello" {
        t.Errorf("argv mutated: %v", out.Args)
    }
}
```

- [ ] **Step 2: Run on non-Linux (or skip)**

If your dev host is Linux, skip this step — the build tag excludes the test. Mention in commit that the file is exercised only on non-Linux CI.

- [ ] **Step 3: Create `agent/exec/jail_other.go`**

```go
// ABOUTME: Non-Linux passthrough stubs for the writable_paths fs-jail (issue #272).
// ABOUTME: ProbeLandlock returns ErrLandlockUnavailable; WrapBashCmd and OpenForWrite no-op.

//go:build !linux
// +build !linux

package exec

import (
    "os"
    "os/exec"
)

// ProbeLandlock on non-Linux always reports Landlock as unavailable.
// The codergen handler refuses to start a session with non-empty
// WritablePaths on this error.
func ProbeLandlock() error {
    return ErrLandlockUnavailable
}

// WrapBashCmd on non-Linux is a passthrough. Reached only if the codergen
// handler somehow installed it despite ProbeLandlock having failed — in
// practice this never happens, but we keep the symbol so the package
// compiles cross-platform.
func WrapBashCmd(cmd *exec.Cmd, anchor string, writable []string) *exec.Cmd {
    return cmd
}

// OpenForWrite on non-Linux returns ErrLandlockUnavailable. Reached only
// if the codergen handler somehow installed WriteOpener despite ProbeLandlock
// having failed.
func OpenForWrite(anchor, relPath string, perm os.FileMode) (*os.File, error) {
    return nil, ErrLandlockUnavailable
}

// RunJailExec on non-Linux is a hard error — the subcommand should never
// be reached on a non-Linux host because the codergen handler refuses to
// install the wrap. If it somehow runs, exit non-zero with a clear message.
func RunJailExec(args []string) int {
    _, _ = os.Stderr.WriteString("tracker __jail-exec: Landlock not supported on this platform\n")
    return 1
}
```

- [ ] **Step 4: Verify Linux build still clean**

```bash
go build ./...
```

Expected: clean. On Linux, this file is excluded; on macOS/Windows, it provides the stubs.

- [ ] **Step 5: Commit**

```bash
git add agent/exec/jail_other.go agent/exec/jail_other_test.go
git commit -m "$(cat <<'EOF'
feat(exec): add non-Linux passthrough stubs for jail

ProbeLandlock returns ErrLandlockUnavailable; WrapBashCmd and OpenForWrite
are passthroughs (codergen handler should never install them when Probe
fails, but the symbols keep the package cross-platform).

//go:build !linux.

Part of #272.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 8: `ValidateWritablePaths` (cross-platform pure function)

**Files:**
- Create: `agent/exec/jail.go` (cross-platform; pure Go path math, no syscalls)
- Create: `agent/exec/jail_test.go`

- [ ] **Step 1: Write failing tests**

```go
// agent/exec/jail_test.go
package exec

import (
    "errors"
    "strings"
    "testing"
)

func TestValidateWritablePaths(t *testing.T) {
    cwd := "/home/user/run"
    cases := []struct {
        name       string
        workingDir string
        globs      []string
        wantErr    string // substring; empty = expect nil error
    }{
        {
            name:       "happy path single glob",
            workingDir: "/home/user/run/work",
            globs:      []string{"workspace/**"},
            wantErr:    "",
        },
        {
            name:       "happy path multiple globs",
            workingDir: "/home/user/run",
            globs:      []string{"workspace/**", ".ai/sprints/**"},
            wantErr:    "",
        },
        {
            name:       "working_dir absolute outside cwd is rejected",
            workingDir: "/tmp/atk",
            globs:      []string{"workspace/**"},
            wantErr:    "working_dir",
        },
        {
            name:       "working_dir parent escape rejected",
            workingDir: "/home/user/run/../../etc",
            globs:      []string{"workspace/**"},
            wantErr:    "working_dir",
        },
        {
            name:       "empty globs is fail-closed",
            workingDir: "/home/user/run",
            globs:      []string{},
            wantErr:    "empty",
        },
        {
            name:       "nil globs is fail-closed",
            workingDir: "/home/user/run",
            globs:      nil,
            wantErr:    "empty",
        },
        {
            name:       "absolute glob entry is rejected",
            workingDir: "/home/user/run",
            globs:      []string{"/etc/**"},
            wantErr:    "escape",
        },
        {
            name:       "tilde glob entry is rejected",
            workingDir: "/home/user/run",
            globs:      []string{"~/secrets/**"},
            wantErr:    "escape",
        },
        {
            name:       "parent-escape glob entry is rejected",
            workingDir: "/home/user/run",
            globs:      []string{"../../etc/**"},
            wantErr:    "escape",
        },
        {
            name:       "malformed brace glob is rejected",
            workingDir: "/home/user/run",
            globs:      []string{"workspace/*.{md"},
            wantErr:    "malformed",
        },
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            err := ValidateWritablePaths(tc.workingDir, tc.globs, cwd)
            if tc.wantErr == "" {
                if err != nil {
                    t.Errorf("ValidateWritablePaths = %v, want nil", err)
                }
                return
            }
            if err == nil {
                t.Fatalf("ValidateWritablePaths = nil, want error containing %q", tc.wantErr)
            }
            if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wantErr)) {
                t.Errorf("ValidateWritablePaths = %v, want error containing %q", err, tc.wantErr)
            }
        })
    }
}

func TestValidateWritablePaths_WrapsForErrorsIs(t *testing.T) {
    err := ValidateWritablePaths("/home/user/run", []string{"/etc/**"}, "/home/user/run")
    if err == nil {
        t.Fatal("expected error for absolute glob")
    }
    // Confirm we can detect this as a path-escape style failure via the
    // sentinel ErrPathEscape (the codergen handler classifies refusals by
    // sentinel identity).
    if !errors.Is(err, ErrPathEscape) {
        t.Errorf("err = %v, want errors.Is(err, ErrPathEscape)", err)
    }
}
```

- [ ] **Step 2: Run, confirm failure**

```bash
go test ./agent/exec/ -run "TestValidateWritablePaths" -v
```

Expected: FAIL — `ValidateWritablePaths` undefined.

- [ ] **Step 3: Create `agent/exec/jail.go`**

```go
// ABOUTME: Cross-platform pure Go helpers for the writable_paths fs-jail (issue #272).
// ABOUTME: ValidateWritablePaths runs at session setup before any syscall jail mechanism.
package exec

import (
    "fmt"
    "path/filepath"
    "strings"
)

// ValidateWritablePaths is the cross-platform gate that runs at session
// setup before the jail is wired. It catches three classes of refusal:
//
//   1. working_dir escapes tracker's process cwd (the "working_dir: /tmp/atk
//      relocation" attack described in the spec § 8.1).
//   2. The glob list is empty (fail-closed; a present-but-empty value is
//      already rejected by dippin's parser, but tracker backstops).
//   3. A glob entry is absolute, starts with ~, escapes the workspace via
//      parent traversal, or has unbalanced brace expansion. These are mostly
//      caught by dippin DIP142, but tracker is the runtime backstop.
//
// Returns ErrPathEscape-wrapped errors for class-1 and class-3; plain error
// for class-2.
func ValidateWritablePaths(workingDir string, globs []string, processCwd string) error {
    if err := validateWorkingDirEscape(workingDir, processCwd); err != nil {
        return err
    }
    if len(globs) == 0 {
        return fmt.Errorf("writable_paths is empty (fail-closed)")
    }
    for _, g := range globs {
        if err := validateGlobEntry(g); err != nil {
            return err
        }
    }
    return nil
}

// validateWorkingDirEscape rejects a working_dir that resolves outside
// processCwd. Catches both absolute paths (e.g. "/tmp/atk") and parent
// escapes (e.g. "../../etc").
func validateWorkingDirEscape(workingDir, processCwd string) error {
    cleanedCwd := filepath.Clean(processCwd)
    var resolved string
    if filepath.IsAbs(workingDir) {
        resolved = filepath.Clean(workingDir)
    } else {
        resolved = filepath.Clean(filepath.Join(cleanedCwd, workingDir))
    }
    if !isSubpathOf(resolved, cleanedCwd) {
        return fmt.Errorf("%w: working_dir %q resolves to %q which escapes the tracker process cwd %q",
            ErrPathEscape, workingDir, resolved, cleanedCwd)
    }
    return nil
}

// isSubpathOf reports whether child is the same as parent or a descendant.
// Both paths must be already cleaned + absolute.
func isSubpathOf(child, parent string) bool {
    if child == parent {
        return true
    }
    sep := string(filepath.Separator)
    if !strings.HasSuffix(parent, sep) {
        parent += sep
    }
    return strings.HasPrefix(child, parent)
}

// validateGlobEntry catches absolute, ~, parent-escape, and malformed-brace
// entries. dippin's DIP142 also catches these; we backstop at runtime.
func validateGlobEntry(g string) error {
    if g == "" {
        return fmt.Errorf("writable_paths entry is empty (fail-closed)")
    }
    if strings.HasPrefix(g, "/") || strings.HasPrefix(g, "~") {
        return fmt.Errorf("%w: writable_paths entry %q is absolute / ~ (must be workspace-relative)",
            ErrPathEscape, g)
    }
    if isWindowsAbsolute(g) {
        return fmt.Errorf("%w: writable_paths entry %q is a Windows absolute path", ErrPathEscape, g)
    }
    cleaned := filepath.Clean(g)
    if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
        return fmt.Errorf("%w: writable_paths entry %q escapes via parent traversal (cleaned: %q)",
            ErrPathEscape, g, cleaned)
    }
    if !balancedBraces(g) {
        return fmt.Errorf("malformed writable_paths entry %q: unbalanced braces (comma-split tore an expansion apart)", g)
    }
    return nil
}

// isWindowsAbsolute matches `C:\foo` or `\\share\foo` shapes build-OS-independently.
func isWindowsAbsolute(s string) bool {
    if len(s) >= 2 && s[1] == ':' && ((s[0] >= 'A' && s[0] <= 'Z') || (s[0] >= 'a' && s[0] <= 'z')) {
        return true
    }
    if strings.HasPrefix(s, `\\`) {
        return true
    }
    return false
}

// balancedBraces returns true when { and } counts match in s. Catches the
// case where a comma-split tore `*.{md,yaml}` into `*.{md` and `yaml}`.
func balancedBraces(s string) bool {
    open := strings.Count(s, "{")
    close := strings.Count(s, "}")
    return open == close
}
```

- [ ] **Step 4: Run, confirm pass**

```bash
go test ./agent/exec/ -run "TestValidateWritablePaths" -v
```

Expected: PASS (10 subtests + the sentinel-wrap test).

- [ ] **Step 5: Commit**

```bash
git add agent/exec/jail.go agent/exec/jail_test.go
git commit -m "$(cat <<'EOF'
feat(exec): add ValidateWritablePaths (cross-platform pure Go)

Catches three classes of refusal at session setup:
1. working_dir escape (the working_dir: /tmp/atk relocation attack)
2. empty glob list (fail-closed; dippin parser also rejects)
3. absolute / ~ / parent-escape / malformed-brace glob entries
   (dippin DIP142 also catches; tracker is the runtime backstop)

Returns ErrPathEscape-wrapped errors for class 1 and 3 so the codergen
handler can classify refusals by sentinel identity.

Pure Go path math; no syscalls; works on every platform.

Part of #272.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 9: `ProbeLandlock` (Linux)

**Files:**
- Create: `agent/exec/jail_linux.go` (start the file; more functions added in later tasks)
- Create: `agent/exec/jail_linux_test.go`

- [ ] **Step 1: Write failing test**

```go
// agent/exec/jail_linux_test.go
//go:build linux
// +build linux

package exec

import (
    "errors"
    "testing"
)

func TestProbeLandlock_OnSupportedKernel(t *testing.T) {
    // GHA ubuntu-latest, Ubuntu 24.04, RHEL 9.4+ all have ABI v3.
    // If this test runs on an older kernel, t.Skip so the suite stays green.
    err := ProbeLandlock()
    if errors.Is(err, ErrLandlockUnavailable) {
        t.Skipf("kernel doesn't support Landlock ABI v3: %v", err)
    }
    if err != nil {
        t.Errorf("ProbeLandlock = %v, want nil on a supported kernel", err)
    }
}
```

- [ ] **Step 2: Run, confirm failure**

```bash
go test ./agent/exec/ -run TestProbeLandlock_OnSupportedKernel -v
```

Expected: FAIL — `ProbeLandlock` undefined on Linux (only the `_other.go` stub exists).

- [ ] **Step 3: Create `agent/exec/jail_linux.go` (initial)**

```go
// ABOUTME: Linux implementation of the writable_paths fs-jail (issue #272).
// ABOUTME: ProbeLandlock + RunJailExec + WrapBashCmd + OpenForWrite; pinned to Landlock ABI v3.

//go:build linux
// +build linux

package exec

import (
    "fmt"

    "github.com/landlock-lsm/go-landlock/landlock"
)

// ProbeLandlock verifies the host kernel supports Landlock ABI v3 (kernel
// 6.7+, June 2023). Called eagerly at session setup. Failure = refuse-to-start.
//
// ABI v3 brings LANDLOCK_ACCESS_FS_REFER (hardlinks across rulesets) and
// LANDLOCK_ACCESS_FS_TRUNCATE; both are needed for the spec's "Bash + children
// bounded" contract.
func ProbeLandlock() error {
    // Use the library's IsSupported check at the V3 level. This issues a
    // lightweight syscall that doesn't actually restrict anything.
    cfg := landlock.V3
    abi, err := cfg.IsSupportedBy() // returns the highest ABI supported by the kernel
    if err != nil {
        return fmt.Errorf("%w: landlock probe failed: %v", ErrLandlockUnavailable, err)
    }
    if abi < 3 {
        return fmt.Errorf("%w: kernel supports Landlock ABI %d, need >= 3 (kernel 6.7+)",
            ErrLandlockUnavailable, abi)
    }
    return nil
}
```

**NOTE for the implementer:** the go-landlock library's exact API for "what's the highest ABI?" may differ from the speculative `IsSupportedBy()` above. Read the library's actual API:
```bash
go doc github.com/landlock-lsm/go-landlock/landlock
```
The library typically exposes `Config.BestEffort()` or similar. Substitute the correct call. The contract is: refuse if ABI < 3. If the library doesn't expose a query API, use a defensive `landlock.V3.RestrictPaths()` on an empty ruleset in a forked-then-discarded subprocess (the spec's fallback). Implement whichever the library actually supports.

- [ ] **Step 4: Run, confirm pass**

```bash
go test ./agent/exec/ -run TestProbeLandlock_OnSupportedKernel -v
```

Expected: PASS on Linux 6.7+ (or SKIP on older kernels).

- [ ] **Step 5: Commit**

```bash
git add agent/exec/jail_linux.go agent/exec/jail_linux_test.go go.mod go.sum
git commit -m "$(cat <<'EOF'
feat(exec): ProbeLandlock for Linux ABI v3 floor

Verifies the host kernel supports Landlock ABI v3 (kernel 6.7+, June 2023).
ABI v3 brings FS_REFER + FS_TRUNCATE — both needed for the spec's "Bash +
children bounded" contract.

Strict; no BestEffort fallback. Refuses to proceed on older kernels.

Part of #272.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Chunk 4 — Bash subprocess enforcement

### Task 10: `WrapBashCmd` argv rewriter

**Files:**
- Modify: `agent/exec/jail_linux.go` (append)
- Modify: `agent/exec/jail_linux_test.go` (append)

- [ ] **Step 1: Write failing test**

Append to `agent/exec/jail_linux_test.go`:

```go
func TestWrapBashCmd_ArgvShape(t *testing.T) {
    cmd := exec.CommandContext(context.Background(), "sh", "-c", "echo hello")
    wrapped := WrapBashCmd(cmd, "/home/user/run", []string{"workspace/**", ".ai/sprints/**"})

    // Expected argv: /proc/self/exe __jail-exec -- /home/user/run workspace/** .ai/sprints/** -- sh -c echo hello
    want := []string{
        "/proc/self/exe", "__jail-exec", "--",
        "/home/user/run", "workspace/**", ".ai/sprints/**",
        "--", "sh", "-c", "echo hello",
    }
    if len(wrapped.Args) != len(want) {
        t.Fatalf("wrapped argv length = %d, want %d (got %v)", len(wrapped.Args), len(want), wrapped.Args)
    }
    for i, a := range want {
        if wrapped.Args[i] != a {
            t.Errorf("arg[%d] = %q, want %q", i, wrapped.Args[i], a)
        }
    }
    if wrapped.Path != "/proc/self/exe" {
        t.Errorf("Path = %q, want /proc/self/exe", wrapped.Path)
    }
}

func TestWrapBashCmd_PreservesContext(t *testing.T) {
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    cmd := exec.CommandContext(ctx, "sh", "-c", "true")
    wrapped := WrapBashCmd(cmd, "/tmp/run", []string{"workspace/**"})
    // The wrapped Cmd should still inherit the ctx — we cancel and expect Wait to surface ctx.Err.
    if err := wrapped.Start(); err != nil {
        t.Skipf("wrapped Start: %v (skip if /proc/self/exe doesn't __jail-exec dispatch yet)", err)
    }
    cancel()
    _ = wrapped.Wait()
    if ctx.Err() == nil {
        t.Error("ctx should be canceled after cancel()")
    }
}
```

- [ ] **Step 2: Run, confirm failure**

```bash
go test ./agent/exec/ -run TestWrapBashCmd -v
```

Expected: FAIL — `WrapBashCmd` not yet defined on Linux.

- [ ] **Step 3: Implement `WrapBashCmd`**

Append to `agent/exec/jail_linux.go`:

```go
// WrapBashCmd rewrites cmd's argv to invoke `/proc/self/exe __jail-exec` with
// the writable_paths jail rules, then the original command after a `--`
// separator.
//
// The wrapped command runs in three stages:
//   1. tracker re-execs itself as `tracker __jail-exec`.
//   2. The __jail-exec child applies Landlock ABI v3 with the static-prefix
//      ancestor directories of each writable_paths glob.
//   3. The child syscall.Exec's into `sh -c <agentCmd>`, replacing its image.
//      Landlock is preserved through exec; bash + all descendants are bounded.
//
// All other Cmd fields (Dir, Env, Stdin/Stdout/Stderr, SysProcAttr, ctx)
// are preserved.
func WrapBashCmd(cmd *exec.Cmd, anchor string, writable []string) *exec.Cmd {
    // Build new argv: /proc/self/exe __jail-exec -- anchor glob... -- origArgs
    newArgs := make([]string, 0, 4+len(writable)+len(cmd.Args))
    newArgs = append(newArgs, "/proc/self/exe", "__jail-exec", "--")
    newArgs = append(newArgs, anchor)
    newArgs = append(newArgs, writable...)
    newArgs = append(newArgs, "--")
    newArgs = append(newArgs, cmd.Args...)

    cmd.Path = "/proc/self/exe"
    cmd.Args = newArgs
    return cmd
}
```

- [ ] **Step 4: Run, confirm pass**

```bash
go test ./agent/exec/ -run TestWrapBashCmd -v
```

Expected: PASS (TestWrapBashCmd_PreservesContext may SKIP because __jail-exec dispatch isn't wired yet).

- [ ] **Step 5: Commit**

```bash
git add agent/exec/jail_linux.go agent/exec/jail_linux_test.go
git commit -m "$(cat <<'EOF'
feat(exec): WrapBashCmd argv rewriter

Rewrites a Bash *exec.Cmd to invoke tracker __jail-exec via /proc/self/exe.
Argv shape: /proc/self/exe __jail-exec -- <anchor> <glob1> ... -- <origArgs>.
The -- separators are unambiguous boundaries for argv parsing in RunJailExec.

All other Cmd fields (Dir, Env, SysProcAttr, ctx) preserved.

Part of #272.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 11: `RunJailExec` self-re-exec target

**Files:**
- Modify: `agent/exec/jail_linux.go` (append)
- Modify: `agent/exec/jail_linux_test.go` (append, with TestMain re-exec idiom)

- [ ] **Step 1: Write failing tests**

Append to `agent/exec/jail_linux_test.go`:

```go
// TestMain dispatches to the jail-exec helper when the test binary is invoked
// as a re-exec child (TRACKER_TEST_JAIL_EXEC=1).
func TestMain(m *testing.M) {
    if os.Getenv("TRACKER_TEST_JAIL_EXEC") == "1" {
        // We are the re-exec child. Args are the same shape RunJailExec
        // expects after the binary path: -- anchor glob... -- cmd...
        os.Exit(RunJailExec(os.Args[1:]))
    }
    os.Exit(m.Run())
}

func TestRunJailExec_DeniesOutsideWrite(t *testing.T) {
    if errors.Is(ProbeLandlock(), ErrLandlockUnavailable) {
        t.Skip("Landlock unavailable on this host")
    }
    anchor := t.TempDir()
    // Try to write outside the jail.
    outsidePath := filepath.Join(t.TempDir(), "escape.txt")
    cmd := exec.Command(os.Args[0], "--", anchor, "workspace/**", "--",
        "sh", "-c", fmt.Sprintf("echo pwned > %s", outsidePath))
    cmd.Env = append(os.Environ(), "TRACKER_TEST_JAIL_EXEC=1")
    out, err := cmd.CombinedOutput()
    if err == nil {
        t.Errorf("re-exec succeeded; expected non-zero exit. Output: %s", out)
    }
    if _, statErr := os.Stat(outsidePath); statErr == nil {
        t.Errorf("file %q exists; jail let the write through. Output: %s", outsidePath, out)
    }
}

func TestRunJailExec_AllowsInsideWrite(t *testing.T) {
    if errors.Is(ProbeLandlock(), ErrLandlockUnavailable) {
        t.Skip("Landlock unavailable on this host")
    }
    anchor := t.TempDir()
    if err := os.MkdirAll(filepath.Join(anchor, "workspace"), 0755); err != nil {
        t.Fatal(err)
    }
    insidePath := filepath.Join(anchor, "workspace", "ok.txt")
    cmd := exec.Command(os.Args[0], "--", anchor, "workspace/**", "--",
        "sh", "-c", fmt.Sprintf("echo allowed > %s", insidePath))
    cmd.Env = append(os.Environ(), "TRACKER_TEST_JAIL_EXEC=1")
    out, err := cmd.CombinedOutput()
    if err != nil {
        t.Fatalf("re-exec failed: %v. Output: %s", err, out)
    }
    contents, err := os.ReadFile(insidePath)
    if err != nil {
        t.Fatalf("inside file not created: %v", err)
    }
    if strings.TrimSpace(string(contents)) != "allowed" {
        t.Errorf("inside file contents = %q, want %q", string(contents), "allowed")
    }
}

func TestRunJailExec_DeniesSymlinkEscape(t *testing.T) {
    if errors.Is(ProbeLandlock(), ErrLandlockUnavailable) {
        t.Skip("Landlock unavailable on this host")
    }
    anchor := t.TempDir()
    if err := os.MkdirAll(filepath.Join(anchor, "workspace"), 0755); err != nil {
        t.Fatal(err)
    }
    // Agent forges a symlink inside the jail pointing outside, then writes through it.
    outsideDir := t.TempDir()
    cmd := exec.Command(os.Args[0], "--", anchor, "workspace/**", "--",
        "sh", "-c", fmt.Sprintf("ln -s %s %s/link && echo pwned > %s/link/escape.txt", outsideDir, filepath.Join(anchor, "workspace"), filepath.Join(anchor, "workspace")))
    cmd.Env = append(os.Environ(), "TRACKER_TEST_JAIL_EXEC=1")
    _, _ = cmd.CombinedOutput() // We don't care about exit code; symlink creation may succeed.
    escapePath := filepath.Join(outsideDir, "escape.txt")
    if _, err := os.Stat(escapePath); err == nil {
        t.Errorf("file %q exists; symlink-escape was not blocked", escapePath)
    }
}
```

- [ ] **Step 2: Run, confirm failure**

```bash
go test ./agent/exec/ -run TestRunJailExec -v
```

Expected: FAIL — `RunJailExec` returns 1 (the stub from `jail_other.go`) on Linux too (since this test file uses `os.Args[0]` and the Linux jail_linux.go doesn't yet define `RunJailExec`). OR FAIL at compile because `RunJailExec` is undefined on Linux when both files are present.

Actually — the build system will pick `jail_linux.go` on Linux and `jail_other.go` on non-Linux. We haven't defined `RunJailExec` in `jail_linux.go` yet, so the Linux build will fail to compile until we add it.

- [ ] **Step 3: Implement `RunJailExec`**

Append to `agent/exec/jail_linux.go`:

```go
// RunJailExec is the entry point for the `tracker __jail-exec` subcommand.
// Argv shape (already stripped of "__jail-exec" by the caller):
//   -- <anchor> <glob1> <glob2> ... -- <cmd> <args>...
//
// The function:
//   1. Parses argv into anchor + globs + command tail.
//   2. Computes Landlock RWDirs from the static-prefix ancestor of each glob
//      (per spec D2; Landlock is path-prefix on resolved paths, not glob-aware).
//   3. Applies Landlock ABI v3 to the current process. go-landlock sets
//      PR_SET_NO_NEW_PRIVS automatically.
//   4. syscall.Exec's into the command tail, replacing the process image.
//      Landlock is preserved through exec; bash + all descendants are bounded.
//
// Returns the process exit code on failure; on success (post-exec), this
// function does not return.
func RunJailExec(args []string) int {
    anchor, globs, cmdArgs, err := parseJailExecArgs(args)
    if err != nil {
        fmt.Fprintf(os.Stderr, "tracker __jail-exec: %v\n", err)
        return 2
    }

    rwDirs := make([]string, 0, len(globs))
    for _, g := range globs {
        rwDirs = append(rwDirs, landlockDirForGlob(anchor, g))
    }

    if err := landlock.V3.RestrictPaths(landlock.RWDirs(rwDirs...)); err != nil {
        fmt.Fprintf(os.Stderr, "tracker __jail-exec: landlock_restrict_self: %v\n", err)
        return 3
    }

    // syscall.Exec replaces the process image. Landlock is preserved.
    if err := syscall.Exec(cmdArgs[0], cmdArgs, os.Environ()); err != nil {
        fmt.Fprintf(os.Stderr, "tracker __jail-exec: exec %q: %v\n", cmdArgs[0], err)
        return 4
    }
    return 0 // unreachable
}

// parseJailExecArgs splits argv into anchor, glob list, and command tail.
// Expected shape: -- anchor glob... -- cmd args...
func parseJailExecArgs(args []string) (anchor string, globs []string, cmdArgs []string, err error) {
    if len(args) < 1 || args[0] != "--" {
        return "", nil, nil, fmt.Errorf("invalid argv: missing leading -- separator")
    }
    args = args[1:] // drop leading --

    // Find the second -- separator.
    sep := -1
    for i, a := range args {
        if a == "--" {
            sep = i
            break
        }
    }
    if sep < 0 {
        return "", nil, nil, fmt.Errorf("invalid argv: missing command -- separator")
    }
    head := args[:sep]
    cmdArgs = args[sep+1:]
    if len(head) < 1 {
        return "", nil, nil, fmt.Errorf("invalid argv: missing anchor")
    }
    if len(cmdArgs) < 1 {
        return "", nil, nil, fmt.Errorf("invalid argv: missing command")
    }
    anchor = head[0]
    globs = head[1:]
    if len(globs) == 0 {
        return "", nil, nil, fmt.Errorf("invalid argv: missing globs")
    }
    return anchor, globs, cmdArgs, nil
}

// landlockDirForGlob returns the directory ancestor of the glob's static
// prefix, joined with the anchor. Per spec D2:
//   workspace/**       → anchor/workspace
//   workspace/out.md   → anchor/workspace
//   .ai/sprints/**     → anchor/.ai/sprints
func landlockDirForGlob(anchor, g string) string {
    idx := strings.IndexAny(g, "*?[{")
    var prefix string
    if idx < 0 {
        prefix = g
    } else {
        prefix = g[:idx]
    }
    dir := filepath.Dir(prefix)
    if dir == "." {
        return anchor
    }
    return filepath.Join(anchor, dir)
}
```

Make sure the imports at the top of `jail_linux.go` include:

```go
import (
    "fmt"
    "os"
    "path/filepath"
    "strings"
    "syscall"

    "github.com/landlock-lsm/go-landlock/landlock"
)
```

- [ ] **Step 4: Run, confirm pass**

```bash
go test ./agent/exec/ -run TestRunJailExec -v
```

Expected: PASS (3 subtests), or SKIP on a host without Landlock.

- [ ] **Step 5: Commit**

```bash
git add agent/exec/jail_linux.go agent/exec/jail_linux_test.go
git commit -m "$(cat <<'EOF'
feat(exec): RunJailExec self-re-exec target

Entry point for `tracker __jail-exec` subcommand. Parses argv into anchor +
globs + command tail; builds Landlock RWDirs from each glob's static-prefix
ancestor directory (per spec D2 two-tier semantic); applies Landlock ABI v3;
syscall.Exec's into the agent command.

TestMain re-exec idiom is used for unit tests — no separate helper binary,
no PATH dependency.

3 red-team tests pass on hosts with Landlock:
- DeniesOutsideWrite: write outside anchor is denied
- AllowsInsideWrite: write inside writable_paths succeeds
- DeniesSymlinkEscape: symlink forged inside, written through, lands outside
  the jail — denied at the kernel layer

Part of #272.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 12: Wire `__jail-exec` dispatch in `cmd/tracker/main.go`

**Files:**
- Modify: `cmd/tracker/main.go`
- Modify: `cmd/tracker/main_test.go` (or create)

- [ ] **Step 1: Read main.go to find the entry point**

```bash
grep -n "func main\|os.Args\|cobra" cmd/tracker/main.go | head -10
```

Find the line where `main()` starts and where cobra is initialized.

- [ ] **Step 2: Insert the dispatch**

In `cmd/tracker/main.go`, at the very top of `main()` (BEFORE cobra initialization), add:

```go
func main() {
    // __jail-exec is an internal subcommand the agent runtime invokes via
    // /proc/self/exe to re-exec itself into a Landlock-sandboxed child for
    // the writable_paths fs-jail (issue #272). It MUST be dispatched before
    // cobra initialization because:
    //   - We don't want cobra to validate flags, surface help, or run hooks.
    //   - The child's job is to apply Landlock and syscall.Exec into sh -c.
    //   - Operators MUST NOT invoke it directly; the __ prefix signals
    //     "internal." See CLAUDE.md § Architecture Gotchas for details.
    if len(os.Args) > 1 && os.Args[1] == "__jail-exec" {
        os.Exit(execpkg.RunJailExec(os.Args[2:]))
    }
    // ... existing main() body ...
}
```

Add the import:

```go
import (
    // ... existing imports ...
    execpkg "github.com/2389-research/tracker/agent/exec"
)
```

(Use `execpkg` alias to avoid conflict with `os/exec`.)

- [ ] **Step 3: Add an end-to-end test (Linux only)**

Create or append to `cmd/tracker/main_test.go`:

```go
//go:build linux
// +build linux

package main

import (
    "errors"
    "os"
    "os/exec"
    "path/filepath"
    "testing"

    trkexec "github.com/2389-research/tracker/agent/exec"
)

func TestJailExecDispatch(t *testing.T) {
    if errors.Is(trkexec.ProbeLandlock(), trkexec.ErrLandlockUnavailable) {
        t.Skip("Landlock unavailable")
    }

    // Build tracker. Use go build into a temp file so we don't depend on
    // a pre-built tracker on PATH.
    bin := filepath.Join(t.TempDir(), "tracker")
    cmd := exec.Command("go", "build", "-o", bin, ".")
    if out, err := cmd.CombinedOutput(); err != nil {
        t.Fatalf("go build: %v. Output: %s", err, out)
    }

    // Invoke tracker __jail-exec with a deny-then-allow scenario.
    anchor := t.TempDir()
    if err := os.MkdirAll(filepath.Join(anchor, "workspace"), 0755); err != nil {
        t.Fatal(err)
    }
    insidePath := filepath.Join(anchor, "workspace", "ok.txt")
    outsidePath := filepath.Join(t.TempDir(), "escape.txt")

    runCmd := exec.Command(bin, "__jail-exec", "--", anchor, "workspace/**", "--",
        "sh", "-c", "echo allowed > "+insidePath+"; echo denied > "+outsidePath+" || true")
    out, err := runCmd.CombinedOutput()
    t.Logf("tracker __jail-exec output: %s; err: %v", out, err)

    if _, err := os.Stat(insidePath); err != nil {
        t.Errorf("inside write was blocked: %v", err)
    }
    if _, err := os.Stat(outsidePath); err == nil {
        t.Errorf("outside write succeeded; jail did not enforce")
    }
}
```

- [ ] **Step 4: Run, confirm pass**

```bash
go test ./cmd/tracker -run TestJailExecDispatch -v
```

Expected: PASS on a Linux 6.7+ host, SKIP otherwise.

- [ ] **Step 5: Verify dispatch doesn't break normal CLI**

```bash
go run ./cmd/tracker --help 2>&1 | head -10
```

Expected: normal help output (no dispatch fired).

- [ ] **Step 6: Commit**

```bash
git add cmd/tracker/main.go cmd/tracker/main_test.go
git commit -m "$(cat <<'EOF'
feat(cmd): dispatch __jail-exec subcommand before cobra init

When tracker is re-exec'd via /proc/self/exe as `tracker __jail-exec ...`,
short-circuit to exec.RunJailExec before cobra initialization. This is the
runtime path for Bash subprocess sandboxing under writable_paths (issue
#272). Operators MUST NOT invoke this directly; the __ prefix signals
"internal."

End-to-end test on Linux confirms the dispatch path enforces both deny
(outside) and allow (inside writable_paths) at the binary level.

Part of #272.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Chunk 5 — In-process write enforcement

### Task 13: `OpenForWrite` (openat2) on Linux

**Files:**
- Modify: `agent/exec/jail_linux.go` (append)
- Modify: `agent/exec/jail_linux_test.go` (append)

- [ ] **Step 1: Write failing tests**

Append to `agent/exec/jail_linux_test.go`:

```go
func TestOpenForWrite_AllowsInsideAnchor(t *testing.T) {
    anchor := t.TempDir()
    f, err := OpenForWrite(anchor, "ok.txt", 0644)
    if err != nil {
        t.Fatalf("OpenForWrite: %v", err)
    }
    defer f.Close()
    if _, err := f.Write([]byte("hello")); err != nil {
        t.Errorf("Write: %v", err)
    }
}

func TestOpenForWrite_RejectsParentEscape(t *testing.T) {
    anchor := t.TempDir()
    _, err := OpenForWrite(anchor, "../escape.txt", 0644)
    if err == nil {
        t.Fatal("OpenForWrite for parent escape = nil error; want refuse")
    }
    if !errors.Is(err, ErrPathEscape) {
        t.Errorf("err = %v, want errors.Is(err, ErrPathEscape)", err)
    }
}

func TestOpenForWrite_RejectsSymlinkEscape(t *testing.T) {
    anchor := t.TempDir()
    // Create a symlink inside the anchor pointing outside.
    outside := t.TempDir()
    linkPath := filepath.Join(anchor, "link")
    if err := os.Symlink(outside, linkPath); err != nil {
        t.Skipf("symlink not supported: %v", err)
    }
    _, err := OpenForWrite(anchor, "link/payload.txt", 0644)
    if err == nil {
        t.Fatal("OpenForWrite through symlink to outside = nil error; want refuse")
    }
    if !errors.Is(err, ErrPathEscape) {
        t.Errorf("err = %v, want errors.Is(err, ErrPathEscape)", err)
    }
}
```

- [ ] **Step 2: Run, confirm failure**

```bash
go test ./agent/exec/ -run TestOpenForWrite -v
```

Expected: FAIL — `OpenForWrite` undefined on Linux.

- [ ] **Step 3: Implement `OpenForWrite`**

Append to `agent/exec/jail_linux.go`:

```go
// OpenForWrite opens (or creates + truncates) a file under anchor for writing,
// using openat2(2) with RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS | RESOLVE_NO_MAGICLINKS.
// The kernel binds path resolution to anchorFD; symlink chains rejected at
// the syscall — no userspace TOCTOU window.
//
// Returns ErrPathEscape (wrapped) when the kernel returns EXDEV / ELOOP /
// EACCES indicating the resolved path is outside anchor.
//
// Used by LocalEnvironment.WriteOpener when SessionConfig.WritablePaths is
// non-empty. The codergen handler installs the configured OpenForWrite
// closure on the env.
func OpenForWrite(anchor, relPath string, perm os.FileMode) (*os.File, error) {
    // Open the anchor dirfd.
    anchorFD, err := unix.Open(anchor, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
    if err != nil {
        return nil, fmt.Errorf("open anchor %q: %w", anchor, err)
    }
    defer unix.Close(anchorFD)

    how := unix.OpenHow{
        Flags:   uint64(unix.O_WRONLY | unix.O_CREAT | unix.O_TRUNC | unix.O_CLOEXEC),
        Mode:    uint64(perm),
        Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
    }
    fd, err := unix.Openat2(anchorFD, relPath, &how)
    if err != nil {
        switch err {
        case unix.EXDEV, unix.ELOOP, unix.EACCES:
            return nil, fmt.Errorf("%w: openat2 %q under %q: %v",
                ErrPathEscape, relPath, anchor, err)
        }
        return nil, fmt.Errorf("openat2 %q under %q: %w", relPath, anchor, err)
    }
    return os.NewFile(uintptr(fd), filepath.Join(anchor, relPath)), nil
}
```

Add `golang.org/x/sys/unix` to imports if not already present. Run `go mod tidy`.

- [ ] **Step 4: Run, confirm pass**

```bash
go test ./agent/exec/ -run TestOpenForWrite -v
```

Expected: PASS (3 subtests).

- [ ] **Step 5: Commit**

```bash
git add agent/exec/jail_linux.go agent/exec/jail_linux_test.go go.mod go.sum
git commit -m "$(cat <<'EOF'
feat(exec): OpenForWrite for in-process tools via openat2

openat2(anchorFD, relPath, RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS |
RESOLVE_NO_MAGICLINKS). Kernel binds path resolution to the anchor FD;
symlink chains rejected at the syscall — no userspace TOCTOU window.

Closes the parallel-branch race vector documented in spec D6: branch A
bash can forge symlinks inside the workdir, but branch B's in-process
Write goes through this helper and the kernel atomic-checks at the syscall.

EXDEV/ELOOP/EACCES from the kernel translate to ErrPathEscape (wrapped).

Part of #272.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Chunk 6 — Codergen integration

### Task 14: `configureJail` helper

**Files:**
- Create: `pipeline/handlers/codergen_jail.go`
- Create: `pipeline/handlers/codergen_jail_test.go`

- [ ] **Step 1: Write failing tests**

```go
// pipeline/handlers/codergen_jail_test.go
package handlers

import (
    "errors"
    "strings"
    "testing"

    "github.com/2389-research/tracker/agent"
    execpkg "github.com/2389-research/tracker/agent/exec"
)

func TestConfigureJail_NoWritablePaths_NoOp(t *testing.T) {
    env := execpkg.NewLocalEnvironment(t.TempDir())
    cfg := agent.SessionConfig{
        WorkingDir:    "./work",
        WritablePaths: nil,
        Backend:       "native",
    }
    enabled, err := configureJail(&cfg, env, "/home/user/run")
    if err != nil {
        t.Fatalf("configureJail = %v, want nil for no writable_paths", err)
    }
    if enabled {
        t.Error("configureJail reported enabled=true with no writable_paths")
    }
    if env.CommandWrapper != nil || env.WriteOpener != nil {
        t.Error("env hooks set when jail not enabled")
    }
}

func TestConfigureJail_RefusesOnClaudeCode(t *testing.T) {
    env := execpkg.NewLocalEnvironment(t.TempDir())
    cfg := agent.SessionConfig{
        WorkingDir:    "./work",
        WritablePaths: []string{"workspace/**"},
        Backend:       "claude-code",
    }
    _, err := configureJail(&cfg, env, "/home/user/run")
    if err == nil {
        t.Fatal("configureJail with claude-code backend = nil error; want refuse")
    }
    if !strings.Contains(err.Error(), "claude-code") {
        t.Errorf("err = %v, want message naming claude-code backend", err)
    }
}

func TestConfigureJail_RefusesOnAcp(t *testing.T) {
    env := execpkg.NewLocalEnvironment(t.TempDir())
    cfg := agent.SessionConfig{
        WorkingDir:    "./work",
        WritablePaths: []string{"workspace/**"},
        Backend:       "acp",
    }
    _, err := configureJail(&cfg, env, "/home/user/run")
    if err == nil {
        t.Fatal("configureJail with acp backend = nil error; want refuse")
    }
}

func TestConfigureJail_RefusesOnInvalidPaths(t *testing.T) {
    env := execpkg.NewLocalEnvironment(t.TempDir())
    cfg := agent.SessionConfig{
        WorkingDir:    "./work",
        WritablePaths: []string{"/etc/**"},
        Backend:       "native",
    }
    _, err := configureJail(&cfg, env, "/home/user/run")
    if !errors.Is(err, execpkg.ErrPathEscape) {
        t.Errorf("err = %v, want errors.Is(err, ErrPathEscape)", err)
    }
}

func TestConfigureJail_RefusesOnNoLandlock_SimulatedNonLinux(t *testing.T) {
    // This test relies on ProbeLandlock failing. On Linux 6.7+ it won't fail,
    // so we skip there. On non-Linux it returns ErrLandlockUnavailable.
    if probeErr := execpkg.ProbeLandlock(); probeErr == nil {
        t.Skip("Landlock available on this host; cannot exercise the no-Landlock refuse path")
    }
    env := execpkg.NewLocalEnvironment(t.TempDir())
    cfg := agent.SessionConfig{
        WorkingDir:    "./work",
        WritablePaths: []string{"workspace/**"},
        Backend:       "native",
    }
    _, err := configureJail(&cfg, env, "/home/user/run")
    if !errors.Is(err, execpkg.ErrLandlockUnavailable) {
        t.Errorf("err = %v, want errors.Is(err, ErrLandlockUnavailable)", err)
    }
}

func TestConfigureJail_HappyPathWiresEnv(t *testing.T) {
    if probeErr := execpkg.ProbeLandlock(); probeErr != nil {
        t.Skipf("Landlock unavailable: %v", probeErr)
    }
    env := execpkg.NewLocalEnvironment(t.TempDir())
    cfg := agent.SessionConfig{
        WorkingDir:    "./work",
        WritablePaths: []string{"workspace/**"},
        Backend:       "native",
    }
    enabled, err := configureJail(&cfg, env, "/home/user/run")
    if err != nil {
        t.Fatalf("configureJail = %v, want nil", err)
    }
    if !enabled {
        t.Error("configureJail reported enabled=false on happy path")
    }
    if env.CommandWrapper == nil {
        t.Error("env.CommandWrapper not set")
    }
    if env.WriteOpener == nil {
        t.Error("env.WriteOpener not set")
    }
}
```

- [ ] **Step 2: Run, confirm failure**

```bash
go test ./pipeline/handlers -run TestConfigureJail -v
```

Expected: FAIL — `configureJail` undefined.

- [ ] **Step 3: Create `pipeline/handlers/codergen_jail.go`**

```go
// ABOUTME: configureJail wires the writable_paths fs-jail into the agent's exec environment.
// ABOUTME: Three refuse-to-start gates: bad paths, unsupported backend, Landlock unavailable.
package handlers

import (
    "fmt"
    "os"
    osexec "os/exec"
    "path/filepath"
    "strings"

    "github.com/2389-research/tracker/agent"
    execpkg "github.com/2389-research/tracker/agent/exec"
)

// configureJail consults cfg.WritablePaths and wires the jail into env when
// the field is non-empty. Returns (enabled, err):
//   - (false, nil) when WritablePaths is empty — no jail, env unchanged.
//   - (false, err) when a refuse-to-start gate fires — session creation halts.
//   - (true, nil) when the jail is fully wired — env.CommandWrapper and
//     env.WriteOpener are populated.
//
// Refusal gates (per spec § 8.4):
//   G1. ValidateWritablePaths returns an error (covers bad working_dir, bad globs).
//   G2. Backend is claude-code or acp (out-of-process; jail can't enforce).
//   G3. ProbeLandlock fails (non-Linux, kernel < 6.7, syscall denied).
func configureJail(cfg *agent.SessionConfig, env *execpkg.LocalEnvironment, processCwd string) (bool, error) {
    if len(cfg.WritablePaths) == 0 {
        return false, nil
    }

    // G1: validate the working_dir + glob shape.
    if err := execpkg.ValidateWritablePaths(cfg.WorkingDir, cfg.WritablePaths, processCwd); err != nil {
        return false, fmt.Errorf("writable_paths validation failed: %w", err)
    }

    // G2: refuse unsupported backends.
    switch cfg.Backend {
    case "", "native":
        // ok
    case "claude-code", "acp":
        return false, fmt.Errorf("writable_paths is not supported on backend %q (only native enforces; see issue #272)", cfg.Backend)
    default:
        // Unknown backend names — also refuse. Better fail-closed than ship a
        // silent no-op on a future backend that doesn't enforce.
        return false, fmt.Errorf("writable_paths refuses unknown backend %q (only native enforces; see issue #272)", cfg.Backend)
    }

    // G3: probe Landlock support.
    if err := execpkg.ProbeLandlock(); err != nil {
        return false, fmt.Errorf("writable_paths requires Landlock: %w", err)
    }

    // Wire the env. The anchor is the absolute resolved WorkingDir.
    var anchor string
    if filepath.IsAbs(cfg.WorkingDir) {
        anchor = filepath.Clean(cfg.WorkingDir)
    } else {
        anchor = filepath.Clean(filepath.Join(processCwd, cfg.WorkingDir))
    }
    globs := append([]string(nil), cfg.WritablePaths...) // defensive copy

    env.CommandWrapper = func(c *osexec.Cmd) *osexec.Cmd {
        return execpkg.WrapBashCmd(c, anchor, globs)
    }
    env.WriteOpener = func(absPath string, perm os.FileMode) (*os.File, error) {
        // LocalEnvironment.WriteFile passes an absolute path; convert to
        // relative-to-anchor for OpenForWrite.
        relPath, relErr := filepath.Rel(anchor, absPath)
        if relErr != nil || strings.HasPrefix(relPath, "..") {
            return nil, fmt.Errorf("%w: %q is outside anchor %q", execpkg.ErrPathEscape, absPath, anchor)
        }
        return execpkg.OpenForWrite(anchor, relPath, perm)
    }
    return true, nil
}
```

**Signature contract verification:** `WrapBashCmd` in Task 10 takes `*exec.Cmd` (from `os/exec`); `LocalEnvironment.CommandWrapper` in Task 5 has the same signature. If either Task 5 or Task 10 ended up using a different concrete type, fix Task 14's wrapper to match. Clean `go build ./...` is the verification.

- [ ] **Step 4: Run, confirm pass**

```bash
go test ./pipeline/handlers -run TestConfigureJail -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pipeline/handlers/codergen_jail.go pipeline/handlers/codergen_jail_test.go
git commit -m "$(cat <<'EOF'
feat(handlers): configureJail wires fs-jail at session setup

Three refuse-to-start gates (spec § 8.4):
  G1. ValidateWritablePaths — bad working_dir or globs
  G2. Backend ∈ {claude-code, acp} or unknown — refuse
  G3. ProbeLandlock — refuse if kernel < 6.7

Happy path wires env.CommandWrapper to WrapBashCmd(anchor, globs) and
env.WriteOpener to an OpenForWrite shim that converts absPath→relPath
before delegating.

Tests cover all four refusal paths plus the happy-path wiring.

Part of #272.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 15: Call `configureJail` from `buildConfig` in codergen handler

**Files:**
- Modify: `pipeline/handlers/codergen.go`

- [ ] **Step 1: Read codergen.go to find buildConfig + the LocalEnvironment construction**

```bash
grep -n "buildConfig\|LocalEnvironment\|NewSession" pipeline/handlers/codergen.go | head -20
```

Find the function that builds `SessionConfig` from node attrs AND the place where `LocalEnvironment` is created (probably near `agent.NewSession(...)`).

- [ ] **Step 2: Wire `WritablePaths` into `SessionConfig`**

In `buildConfig` (or wherever the typed `AgentConfig` is read), find where existing typed fields are copied to `SessionConfig`. Add:

```go
sessionCfg.WritablePaths = agentCfg.WritablePaths
```

- [ ] **Step 3: Wire `configureJail` into session creation**

Find where the `LocalEnvironment` is constructed and `agent.NewSession` is called. Before `NewSession`, add:

```go
processCwd, err := os.Getwd()
if err != nil {
    return nil, fmt.Errorf("get tracker cwd: %w", err)
}
_, jailErr := configureJail(&sessionCfg, env, processCwd)
if jailErr != nil {
    return nil, jailErr // refuse-to-start; surfaces as EventNodeFailed
}
```

(Adapt to the actual variable names — `env`, `sessionCfg` are illustrative.)

- [ ] **Step 4: Build clean**

```bash
go build ./...
```

Expected: clean.

- [ ] **Step 5: Run the existing codergen tests**

```bash
go test ./pipeline/handlers -short
```

Expected: all green (no behavior change for agents without writable_paths).

- [ ] **Step 6: Commit**

```bash
git add pipeline/handlers/codergen.go
git commit -m "$(cat <<'EOF'
feat(handlers): wire configureJail into codergen session setup

buildConfig populates SessionConfig.WritablePaths from the typed
AgentNodeConfig accessor; configureJail runs before agent.NewSession to
install the env hooks (or refuse-to-start). All three gates (paths,
backend, Landlock) surface as EventNodeFailed pre-LLM-token.

Part of #272.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Chunk 7 — End-to-end tests

### Task 16: `TestWritablePathsEnforcement` (table-driven)

**Files:**
- Create: `pipeline/handlers/writable_paths_e2e_test.go` (Linux only)

- [ ] **Step 1: Write the table-driven enforcement test**

```go
//go:build linux
// +build linux

package handlers

import (
    "errors"
    "os"
    "path/filepath"
    "testing"

    "github.com/2389-research/tracker/agent"
    execpkg "github.com/2389-research/tracker/agent/exec"
)

func TestWritablePathsEnforcement(t *testing.T) {
    if err := execpkg.ProbeLandlock(); err != nil {
        t.Skipf("Landlock unavailable: %v", err)
    }

    cases := []struct {
        name       string
        agentBash  string // what the agent's Bash does
        outsideRel string // relative path outside the anchor that should NOT exist after
        insideRel  string // relative path inside the anchor that should exist
    }{
        {
            name:       "direct out-of-jail write denied",
            agentBash:  "echo pwned > %s/escape.txt",
            outsideRel: "outside",
        },
        {
            name:       "child process (cargo build sim) write denied",
            agentBash:  "sh -c 'echo pwned > %s/escape.txt'",
            outsideRel: "outside",
        },
        {
            name:       "in-jail write succeeds",
            agentBash:  "echo allowed > %s/ok.txt",
            insideRel:  "workspace/ok.txt",
        },
    }

    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            anchor := t.TempDir()
            workspace := filepath.Join(anchor, "workspace")
            outsideDir := filepath.Join(t.TempDir(), "outside")
            if err := os.MkdirAll(workspace, 0755); err != nil {
                t.Fatal(err)
            }
            if err := os.MkdirAll(outsideDir, 0755); err != nil {
                t.Fatal(err)
            }

            env := execpkg.NewLocalEnvironment(anchor)
            cfg := agent.SessionConfig{
                WorkingDir:    anchor,
                WritablePaths: []string{"workspace/**"},
                Backend:       "native",
            }
            _, err := configureJail(&cfg, env, anchor)
            if err != nil {
                t.Fatalf("configureJail: %v", err)
            }

            // Build the command. We can't easily run a real agent loop here,
            // so we directly invoke a sh -c via the wrapped env.
            var cmdStr string
            if tc.outsideRel != "" {
                outsideAbs := filepath.Join(outsideDir, "escape.txt")
                cmdStr = fmt.Sprintf(tc.agentBash, outsideDir)
                _ = outsideAbs
            }
            if tc.insideRel != "" {
                cmdStr = fmt.Sprintf(tc.agentBash, workspace)
            }
            _, _ = env.ExecCommand(t.Context(), "sh", []string{"-c", cmdStr}, 5*time.Second)

            if tc.outsideRel != "" {
                escapePath := filepath.Join(outsideDir, "escape.txt")
                if _, err := os.Stat(escapePath); err == nil {
                    t.Errorf("outside write succeeded: %s exists", escapePath)
                }
            }
            if tc.insideRel != "" {
                okPath := filepath.Join(anchor, tc.insideRel)
                if _, err := os.Stat(okPath); err != nil {
                    t.Errorf("inside write blocked: %v", err)
                }
            }
        })
    }
}
```

(Adapt fmt/time/context imports as needed.)

- [ ] **Step 2: Run, confirm pass**

```bash
go test ./pipeline/handlers -run TestWritablePathsEnforcement -v
```

Expected: PASS (3 subtests on Linux 6.7+, SKIP otherwise).

- [ ] **Step 3: Commit**

```bash
git add pipeline/handlers/writable_paths_e2e_test.go
git commit -m "$(cat <<'EOF'
test(handlers): TestWritablePathsEnforcement table-driven E2E

Covers spec § 6 test items 1 (red-team direct) and 2 (symlink escape via
Bash) in one table. Three rows: direct out-of-jail write denied, child
process write denied, in-jail write succeeds.

Linux only; skipped on non-Linux hosts.

Part of #272.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 17: `TestWorkingDirRelocationRefused`

**Files:**
- Modify: `pipeline/handlers/writable_paths_e2e_test.go` (append)

- [ ] **Step 1: Write the test**

Append:

```go
func TestWorkingDirRelocationRefused(t *testing.T) {
    if err := execpkg.ProbeLandlock(); err != nil {
        t.Skipf("Landlock unavailable: %v", err)
    }
    processCwd := t.TempDir()
    env := execpkg.NewLocalEnvironment(processCwd)
    cfg := agent.SessionConfig{
        WorkingDir:    "/tmp/atk",
        WritablePaths: []string{"workspace/**"},
        Backend:       "native",
    }
    _, err := configureJail(&cfg, env, processCwd)
    if err == nil {
        t.Fatal("configureJail with working_dir: /tmp/atk = nil error; want refuse")
    }
    if !errors.Is(err, execpkg.ErrPathEscape) {
        t.Errorf("err = %v, want errors.Is(err, ErrPathEscape)", err)
    }
}
```

- [ ] **Step 2: Run, confirm pass**

```bash
go test ./pipeline/handlers -run TestWorkingDirRelocationRefused -v
```

- [ ] **Step 3: Commit**

```bash
git add pipeline/handlers/writable_paths_e2e_test.go
git commit -m "test(handlers): TestWorkingDirRelocationRefused (#272)"
```

---

### Task 18: `TestWritablePathsFailClosed` (table-driven)

**Files:**
- Modify: `pipeline/handlers/writable_paths_e2e_test.go` (append)

- [ ] **Step 1: Write the table test**

```go
func TestWritablePathsFailClosed(t *testing.T) {
    cases := []struct {
        name    string
        cfg     agent.SessionConfig
        wantSub string
    }{
        {
            name: "empty list",
            cfg: agent.SessionConfig{
                WorkingDir:    ".",
                WritablePaths: []string{},
                Backend:       "native",
            },
            wantSub: "empty",
        },
        {
            name: "malformed brace",
            cfg: agent.SessionConfig{
                WorkingDir:    ".",
                WritablePaths: []string{"workspace/*.{md"},
                Backend:       "native",
            },
            wantSub: "malformed",
        },
        {
            name: "Landlock unavailable (only meaningful on non-Linux or old kernel)",
            cfg: agent.SessionConfig{
                WorkingDir:    ".",
                WritablePaths: []string{"workspace/**"},
                Backend:       "native",
            },
            wantSub: "landlock",
        },
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            if tc.name == "Landlock unavailable (only meaningful on non-Linux or old kernel)" {
                if err := execpkg.ProbeLandlock(); err == nil {
                    t.Skip("Landlock available; cannot exercise this refusal path")
                }
            }
            env := execpkg.NewLocalEnvironment(t.TempDir())
            _, err := configureJail(&tc.cfg, env, "/home/user/run")
            if err == nil {
                t.Fatal("configureJail = nil; want refuse")
            }
            if !strings.Contains(strings.ToLower(err.Error()), tc.wantSub) {
                t.Errorf("err = %v, want substring %q", err, tc.wantSub)
            }
        })
    }
}
```

- [ ] **Step 2: Run, confirm pass; commit**

```bash
go test ./pipeline/handlers -run TestWritablePathsFailClosed -v
git add pipeline/handlers/writable_paths_e2e_test.go
git commit -m "test(handlers): TestWritablePathsFailClosed table (#272)"
```

---

### Task 19: `TestBranchEnforcesResolvedPaths`

**Files:**
- Modify: `pipeline/handlers/writable_paths_e2e_test.go` (append)

- [ ] **Step 1: Write the test**

```go
func TestBranchEnforcesResolvedPaths(t *testing.T) {
    if err := execpkg.ProbeLandlock(); err != nil {
        t.Skipf("Landlock unavailable: %v", err)
    }
    // Branch with already-resolved WritablePaths (dippin filled it in via
    // inherit-on-empty at the IR layer). Tracker enforces what dippin gave it.
    anchor := t.TempDir()
    workspace := filepath.Join(anchor, "workspace")
    if err := os.MkdirAll(workspace, 0755); err != nil {
        t.Fatal(err)
    }
    env := execpkg.NewLocalEnvironment(anchor)
    cfg := agent.SessionConfig{
        WorkingDir:    anchor,
        WritablePaths: []string{"workspace/**"},
        Backend:       "native",
    }
    enabled, err := configureJail(&cfg, env, anchor)
    if err != nil {
        t.Fatalf("configureJail: %v", err)
    }
    if !enabled {
        t.Fatal("jail not enabled")
    }
    okPath := filepath.Join(workspace, "ok.txt")
    _, err = env.ExecCommand(context.Background(), "sh",
        []string{"-c", fmt.Sprintf("echo allowed > %s", okPath)}, 5*time.Second)
    if err != nil {
        t.Errorf("inside write failed: %v", err)
    }
    if _, statErr := os.Stat(okPath); statErr != nil {
        t.Errorf("inside file not created: %v", statErr)
    }
}
```

- [ ] **Step 2: Run, confirm pass; commit**

```bash
go test ./pipeline/handlers -run TestBranchEnforcesResolvedPaths -v
git add pipeline/handlers/writable_paths_e2e_test.go
git commit -m "test(handlers): TestBranchEnforcesResolvedPaths (#272)"
```

---

### Task 20: `TestParallelBranchSymlinkRace`

**Files:**
- Modify: `pipeline/handlers/parallel_test.go`

- [ ] **Step 1: Write the test**

Append to `pipeline/handlers/parallel_test.go`:

```go
//go:build linux
// +build linux

func TestParallelBranchSymlinkRace(t *testing.T) {
    if err := execpkg.ProbeLandlock(); err != nil {
        t.Skipf("Landlock unavailable: %v", err)
    }
    anchor := t.TempDir()
    workspaceA := filepath.Join(anchor, "branchA")
    workspaceB := filepath.Join(anchor, "branchB")
    if err := os.MkdirAll(workspaceA, 0755); err != nil {
        t.Fatal(err)
    }
    if err := os.MkdirAll(workspaceB, 0755); err != nil {
        t.Fatal(err)
    }

    // The branches share `anchor` per pipeline/handlers/parallel.go:162.
    // Branch A's Bash runs a tight symlink-forge loop inside its workspace.
    // Branch B's in-process Write tries to write through the forged symlink.
    //
    // The verified vector (spec D6): without openat2, Branch B's Write would
    // race the symlink swap. With openat2(RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS),
    // the kernel atomic-rejects on symlink-during-resolution.

    outsideDir := t.TempDir()

    envA := execpkg.NewLocalEnvironment(anchor)
    cfgA := agent.SessionConfig{
        WorkingDir:    anchor,
        WritablePaths: []string{"branchA/**"},
        Backend:       "native",
    }
    if _, err := configureJail(&cfgA, envA, anchor); err != nil {
        t.Fatalf("configureJail A: %v", err)
    }

    envB := execpkg.NewLocalEnvironment(anchor)
    cfgB := agent.SessionConfig{
        WorkingDir:    anchor,
        WritablePaths: []string{"branchB/**"},
        Backend:       "native",
    }
    if _, err := configureJail(&cfgB, envB, anchor); err != nil {
        t.Fatalf("configureJail B: %v", err)
    }

    // Goroutine A: forges symlinks in a loop.
    stop := make(chan struct{})
    forgeDone := make(chan struct{})
    go func() {
        defer close(forgeDone)
        for {
            select {
            case <-stop:
                return
            default:
            }
            _, _ = envA.ExecCommand(context.Background(), "sh",
                []string{"-c", fmt.Sprintf("ln -sfn %s %s/share", outsideDir, workspaceA)}, 2*time.Second)
        }
    }()

    // Goroutine B: races writes through any "share" path it can see.
    // Even if A's symlink ends up in branchA/share pointing outside, B's
    // writes are against branchB/something — they shouldn't even reach the
    // outsideDir. But if B's tools EVER follow a symlink and end up writing
    // outside, the test fails.
    for i := 0; i < 200; i++ {
        relPath := fmt.Sprintf("branchB/payload-%d.txt", i)
        _ = envB.WriteFile(relPath, []byte("ok"), 0644)
    }

    close(stop)
    <-forgeDone

    entries, _ := os.ReadDir(outsideDir)
    if len(entries) > 0 {
        t.Errorf("outsideDir has %d entries; race let a write through. Entries: %v", len(entries), entries)
    }
}
```

- [ ] **Step 2: Run, confirm pass; commit**

```bash
go test ./pipeline/handlers -run TestParallelBranchSymlinkRace -v -race
git add pipeline/handlers/parallel_test.go
git commit -m "test(handlers): TestParallelBranchSymlinkRace (#272)"
```

---

### Task 21: `TestBackendRefuseToStart`

**Files:**
- Modify: `pipeline/handlers/codergen_jail_test.go` (already created in Task 14; this is the existing test re-verified)

- [ ] **Step 1: Verify the backend refuse tests from Task 14 still cover the spec contract**

```bash
go test ./pipeline/handlers -run "TestConfigureJail_RefusesOnClaudeCode|TestConfigureJail_RefusesOnAcp" -v
```

Expected: PASS.

- [ ] **Step 2: Tag with a verification commit if any docs were missed**

If no doc/comment changes needed, skip commit. The test coverage already exists in Task 14.

---

## Chunk 8 — Documentation

### Task 22: CLAUDE.md paragraph

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Append paragraph under "Architecture Gotchas"**

```markdown
### `tracker __jail-exec` internal subcommand (#272)

When `writable_paths` is set on an agent node, tracker re-execs itself via
`/proc/self/exe __jail-exec -- <anchor> <globs> -- sh -c <cmd>` to apply
Linux Landlock to Bash subprocesses. The dispatch happens in `cmd/tracker/main.go`
BEFORE cobra initialization. Operators MUST NOT invoke `__jail-exec` directly —
the `__` prefix signals "internal." The helper applies Landlock ABI v3, then
`syscall.Exec`s into `sh -c <cmd>`, preserving Landlock through exec so the
agent's bash and all descendants (cargo, rustc, child shells) are bounded by
the writable_paths globs at the kernel layer. See
`docs/superpowers/specs/2026-06-01-issue-272-writable-paths-enforcement-design.md`
for the full design.
```

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs(claude-md): add __jail-exec entry under Architecture Gotchas (#272)"
```

---

### Task 23: site/static/skill.md paragraph

**Files:**
- Modify: `site/static/skill.md`

- [ ] **Step 1: Append section**

```markdown
### `writable_paths` runtime enforcement (#272)

Agents authored with `writable_paths` in the dippin workflow have their file
mutations bounded at the runtime layer:

- **In-process tools** (`Write`, `Edit`, `ApplyPatch`) enforce the *exact*
  writable_paths globs via `openat2(RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS)`
  against a session-root file descriptor. Symlink chains are rejected at the
  kernel syscall.
- **Bash subprocess** (and every process it spawns) is bounded at the
  *directory ancestor* of each glob's static prefix via Linux Landlock LSM
  (kernel 6.7+, ABI v3). For directory-scoped globs (`workspace/**`,
  `.ai/sprints/**`) the two-tier enforcement converges; for file-scoped globs
  (`workspace/out.md`) Bash can write any file under `workspace/` — strip
  Bash from `tool_access` for exact-file Bash bounding.

**Refuse-to-start** when: `working_dir` resolves outside the tracker run; a
glob is malformed or escapes the workspace; backend is `claude-code` or
`acp` (out-of-process; tracker cannot sandbox); host lacks Linux Landlock
ABI v3.

**Residual escape classes** (not bounded by `writable_paths`): network egress
(curl, cargo crate fetches); reads / exfil-by-read; content within an allowed
path (the agent can still poison `workspace/Cargo.toml` if `writable_paths:
workspace/**`); hardlinks beyond what Landlock ABI v3's `FS_REFER` covers;
inherited FDs from before the jail was applied; `/proc/self/*` re-entry
beyond what `RESOLVE_NO_MAGICLINKS` covers.

**Version-skew safety**: dippin v0.35.0+ pins `requires tracker ≥ <tag>` as a
safety requirement. An older tracker that doesn't recognize `writable_paths`
refuses to run rather than silently ignoring the field.
```

- [ ] **Step 2: Commit**

```bash
git add site/static/skill.md
git commit -m "docs(skill): document writable_paths runtime enforcement (#272)"
```

---

### Task 24: CHANGELOG.md `[Unreleased]` entry

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Add the entry**

Under `## [Unreleased]` (or create the section if missing):

```markdown
### Added

- **`writable_paths` fs-jail enforcement** (closes #272, paired with dippin v0.35.0 / #75). Agents with `writable_paths` declared in the workflow now have a runtime write jail bounding all file mutations (Write/Edit/ApplyPatch AND Bash + descendants) to the declared globs. **Linux-only** (kernel 6.7+ for Landlock ABI v3); macOS/Windows/older Linux refuse-to-start when `writable_paths` is set. claude-code/acp backends also refuse-to-start (out-of-process backends tracker cannot sandbox). In-process tools enforce the *exact* globs via `openat2(RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS)`; Bash subprocess is bounded at the *directory ancestors* of each glob's static prefix via Linux Landlock — for the 5 motivating sentinel-writer adopters these are identical because their globs are directory-scoped.

### Library-API delta

- `pipeline.AgentNodeConfig` gains `WritablePaths []string` (typed accessor).
- `agent.SessionConfig` gains `WritablePaths []string`.
- `agent/exec.LocalEnvironment` gains optional `CommandWrapper` and `WriteOpener` function fields.
- New package functions in `agent/exec/`: `WrapBashCmd`, `RunJailExec`, `ProbeLandlock`, `ValidateWritablePaths`, `OpenForWrite`. Linux-only; non-Linux builds get passthrough stubs.

### Operator notes

- A new internal subcommand `tracker __jail-exec` exists for the runtime to re-exec into a Landlock-sandboxed child for Bash invocations under writable_paths. Operators MUST NOT invoke it directly; it's documented for transparency only.
- Residual escape classes (not bounded by writable_paths): network egress (`curl`, `cargo` crate fetches), reads / exfil-by-read, content within an allowed path. Narrow globs are the strongest posture.

### Requires

- `dippin-lang vX.Y.Z` (the paired v0.35.0 release with the `WritablePaths` IR field). During dev, the worktree pins `@latest` against dippin's `main`; the release PR bumps to the tagged version and bumps `PinnedDippinVersion` in lockstep. **Without a tracker ≥ this tag, dippin v0.35.0's `writable_paths` field is unenforced — the paired tracker fail-closes on the field, so an unpinned/older tracker refuses to run rather than silently ignore.**
```

- [ ] **Step 2: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs(changelog): add writable_paths enforcement entry (#272)"
```

---

## Final verification

After all chunks complete:

- [ ] `go build ./...` clean
- [ ] `go test ./... -short` — all packages pass
- [ ] `go test ./agent/exec ./pipeline/handlers -race -count=1` — no races (covers the parallel-branch race test)
- [ ] `dippin doctor examples/*.dip` — A grade preserved (writable_paths is opt-in; no example sets it yet)
- [ ] Manual smoke (Linux): a `.dip` with `writable_paths: workspace/**` on an agent + `--backend native`. The agent attempts to write outside via in-process Write AND via Bash; both denied; audit row records the rejection.
- [ ] Manual smoke (macOS or older Linux): the same `.dip` refuses-to-start with `ErrLandlockUnavailable` and a clear operator-facing message.
- [ ] Self-check: `git log origin/main..HEAD --oneline | wc -l` shows ~24 atomic commits.

Hand off to release PR sequence per spec § 13.
