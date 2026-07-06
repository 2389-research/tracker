# SWE-bench Harness Review Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix all critical and high-impact important issues identified by the 8-expert panel review of `cmd/tracker-swebench/`.

**Architecture:** Fixes are ordered by dependency — security/correctness issues first (shell injection, diff capture, cache path), then robustness (exit codes, cleanup, resource limits), then quality-of-life (system prompt, UX, docs). Each task is independent and produces a working, testable commit.

**Tech Stack:** Go 1.24+, Docker CLI, `encoding/json`, `os/exec`, `regexp`

---

## File Map

| File | Responsibility | Tasks |
|------|---------------|-------|
| `cmd/tracker-swebench/docker.go` | Container lifecycle, shell commands, diff capture | 1, 2, 3, 4, 5, 6, 7 |
| `cmd/tracker-swebench/docker_test.go` | Docker helper unit tests | 1, 2, 3, 4, 5, 6, 7 |
| `cmd/tracker-swebench/main.go` | CLI entry, orchestration loop, signal handling | 3, 5, 6, 7, 8, 9, 11 |
| `cmd/tracker-swebench/results.go` | Prediction writer, resume logic, stats | 8 |
| `cmd/tracker-swebench/results_test.go` | Results writer tests | 8 |
| `cmd/tracker-swebench/dataset.go` | Dataset parser, instance validation | 9 |
| `cmd/tracker-swebench/dataset_test.go` | Dataset parser tests | 9 |
| `cmd/tracker-swebench/agent-runner/main.go` | In-container agent config, system prompt | 10, 12 |
| `cmd/tracker-swebench/agent-runner/main_test.go` | Agent-runner tests | 10, 12 |
| `cmd/tracker-swebench/agent-runner/providers.go` | Provider adapter constructors | 10 |
| `cmd/tracker-swebench/build.sh` | Cross-compile + docker build | 4 |
| `cmd/tracker-swebench/Dockerfile` | Base Docker image | 4 |
| `llm/catalog.go` | Model registry | 12 |

---

### Task 1: Fix shell injection in `buildCloneCmd`

**Criticality:** Critical — dataset-controlled values concatenated into `sh -c` string.

**Files:**
- Modify: `cmd/tracker-swebench/docker.go:29-39`
- Modify: `cmd/tracker-swebench/docker.go:188-195` (call site in `RunInstance`)
- Modify: `cmd/tracker-swebench/docker_test.go:18-70`

- [ ] **Step 1: Write failing test for safe clone commands**

In `docker_test.go`, replace the existing `TestBuildCloneCmd` and `TestBuildCloneCmd_NoCache` with tests that verify the new signature returns two separate command slices (clone + checkout) instead of a single `sh -c` string:

```go
func TestBuildCloneCommands(t *testing.T) {
	clone, checkout := buildCloneCommands(
		"https://github.com/django/django.git",
		"abc123",
		"/workspace",
		"/cache/django_django.git",
	)

	// Clone command must NOT use sh -c.
	if clone[0] == "sh" {
		t.Error("clone command must not use sh -c")
	}
	if clone[0] != "git" {
		t.Errorf("clone[0] = %q, want \"git\"", clone[0])
	}

	// Must contain --reference with the bare repo path.
	found := false
	for i, arg := range clone {
		if arg == "--reference" && i+1 < len(clone) && clone[i+1] == "/cache/django_django.git" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected --reference /cache/django_django.git in clone args: %v", clone)
	}

	// Must contain --dissociate.
	hasDissociate := false
	for _, arg := range clone {
		if arg == "--dissociate" {
			hasDissociate = true
		}
	}
	if !hasDissociate {
		t.Errorf("expected --dissociate in clone args: %v", clone)
	}

	// Must end with repoURL and workDir.
	if clone[len(clone)-2] != "https://github.com/django/django.git" {
		t.Errorf("expected repo URL as second-to-last arg, got %q", clone[len(clone)-2])
	}
	if clone[len(clone)-1] != "/workspace" {
		t.Errorf("expected workDir as last arg, got %q", clone[len(clone)-1])
	}

	// Checkout must be git -C workDir checkout commit.
	expected := []string{"git", "-C", "/workspace", "checkout", "abc123"}
	if len(checkout) != len(expected) {
		t.Fatalf("checkout = %v, want %v", checkout, expected)
	}
	for i := range expected {
		if checkout[i] != expected[i] {
			t.Errorf("checkout[%d] = %q, want %q", i, checkout[i], expected[i])
		}
	}
}

func TestBuildCloneCommands_NoCache(t *testing.T) {
	clone, checkout := buildCloneCommands(
		"https://github.com/django/django.git",
		"abc123",
		"/workspace",
		"",
	)

	if clone[0] != "git" {
		t.Errorf("clone[0] = %q, want \"git\"", clone[0])
	}
	for _, arg := range clone {
		if arg == "--reference" {
			t.Error("expected no --reference flag when cachePath is empty")
		}
		if arg == "--dissociate" {
			t.Error("expected no --dissociate when cachePath is empty")
		}
	}

	if checkout[0] != "git" {
		t.Errorf("checkout[0] = %q, want \"git\"", checkout[0])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./cmd/tracker-swebench/ -run TestBuildCloneCommands -v`
Expected: FAIL — `buildCloneCommands` is undefined.

- [ ] **Step 3: Implement `buildCloneCommands`**

Replace the old `buildCloneCmd` function in `docker.go:29-39` with:

```go
// buildCloneCommands returns two safe argument slices: one for git clone, one
// for git checkout. No shell is involved — arguments are passed directly to
// exec, eliminating injection via dataset-controlled values.
// When cachePath is non-empty, --reference and --dissociate flags are added
// for local object reuse without creating fragile alternates dependencies.
func buildCloneCommands(repoURL, commit, workDir, cachePath string) (cloneArgs []string, checkoutArgs []string) {
	cloneArgs = []string{"git", "clone"}
	if cachePath != "" {
		cloneArgs = append(cloneArgs, "--reference", cachePath, "--dissociate")
	}
	cloneArgs = append(cloneArgs, repoURL, workDir)

	checkoutArgs = []string{"git", "-C", workDir, "checkout", commit}
	return cloneArgs, checkoutArgs
}
```

- [ ] **Step 4: Update `RunInstance` call site**

In `docker.go`, update the clone step in `RunInstance` (around line 188-195). Replace:

```go
cachePath := ""
if r.CacheDir != "" {
    cachePath = "/cache"
}
cloneCmd := buildCloneCmd(inst.RepoURL(), inst.BaseCommit, workDir, cachePath)
if err = dockerExec(ctx, name, nil, cloneCmd...); err != nil {
    return "", AgentSummary{}, fmt.Errorf("clone repo: %w", err)
}
```

With:

```go
cachePath := ""
if r.CacheDir != "" {
    cachePath = "/cache/" + strings.ReplaceAll(inst.Repo, "/", "_") + ".git"
}
cloneArgs, checkoutArgs := buildCloneCommands(inst.RepoURL(), inst.BaseCommit, workDir, cachePath)
if err = dockerExec(ctx, name, nil, cloneArgs...); err != nil {
    return "", AgentSummary{}, fmt.Errorf("clone repo: %w", err)
}
if err = dockerExec(ctx, name, nil, checkoutArgs...); err != nil {
    return "", AgentSummary{}, fmt.Errorf("checkout commit: %w", err)
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./cmd/tracker-swebench/ -run TestBuildCloneCommands -v`
Expected: PASS

- [ ] **Step 6: Remove old tests for deleted function**

Delete `TestBuildCloneCmd` and `TestBuildCloneCmd_NoCache` from `docker_test.go` (they reference the removed `buildCloneCmd` function).

- [ ] **Step 7: Run full test suite**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./cmd/tracker-swebench/... -v`
Expected: All tests PASS.

- [ ] **Step 8: Commit**

```bash
git add cmd/tracker-swebench/docker.go cmd/tracker-swebench/docker_test.go
git commit -m "fix(swebench): eliminate shell injection in buildCloneCmd

Replace sh -c string concatenation with separate git clone and git
checkout argument slices passed directly to exec. Adds --dissociate
to prevent fragile alternates references. Fixes --reference path to
point to the actual bare repo inside the cache mount, not the parent
directory."
```

---

### Task 2: Fix `git diff` to capture all changes including new files

**Criticality:** Critical — current `git diff` misses untracked files and staged changes.

**Files:**
- Modify: `cmd/tracker-swebench/docker.go:216-221`

- [ ] **Step 1: Write a test for the new `capturePatch` helper**

Add to `docker_test.go`:

```go
func TestCapturePatchCommands(t *testing.T) {
	addArgs, diffArgs := capturePatchCommands("/workspace")

	// git add -A in workDir
	expectedAdd := []string{"git", "-C", "/workspace", "add", "-A"}
	if len(addArgs) != len(expectedAdd) {
		t.Fatalf("addArgs = %v, want %v", addArgs, expectedAdd)
	}
	for i := range expectedAdd {
		if addArgs[i] != expectedAdd[i] {
			t.Errorf("addArgs[%d] = %q, want %q", i, addArgs[i], expectedAdd[i])
		}
	}

	// git diff HEAD in workDir
	expectedDiff := []string{"git", "-C", "/workspace", "diff", "HEAD"}
	if len(diffArgs) != len(expectedDiff) {
		t.Fatalf("diffArgs = %v, want %v", diffArgs, expectedDiff)
	}
	for i := range expectedDiff {
		if diffArgs[i] != expectedDiff[i] {
			t.Errorf("diffArgs[%d] = %q, want %q", i, diffArgs[i], expectedDiff[i])
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./cmd/tracker-swebench/ -run TestCapturePatchCommands -v`
Expected: FAIL — `capturePatchCommands` is undefined.

- [ ] **Step 3: Add `capturePatchCommands` helper**

Add to `docker.go`:

```go
// capturePatchCommands returns two argument slices: git add -A (to stage all
// changes including new files) and git diff HEAD (to produce a diff of all
// changes vs the original checkout commit).
func capturePatchCommands(workDir string) (addArgs []string, diffArgs []string) {
	addArgs = []string{"git", "-C", workDir, "add", "-A"}
	diffArgs = []string{"git", "-C", workDir, "diff", "HEAD"}
	return addArgs, diffArgs
}
```

- [ ] **Step 4: Update `RunInstance` to use two-step diff capture**

Replace the diff capture block in `RunInstance` (around line 216-221):

```go
// Step 6: Capture the git diff (even on agent timeout/failure, capture partial diff).
diffOutput, diffErr := dockerExecOutput(ctx, name, "git", "-C", workDir, "diff")
```

With:

```go
// Step 6: Stage all changes and capture diff vs HEAD (includes new files).
// Use a fresh context so we still capture the patch even if parent ctx is cancelled.
diffCtx, diffCancel := context.WithTimeout(context.Background(), 30*time.Second)
defer diffCancel()

addArgs, diffCmdArgs := capturePatchCommands(workDir)
// Stage all changes (including untracked new files).
if addErr := dockerExec(diffCtx, name, nil, addArgs...); addErr != nil {
    log.Printf("[%s] git add -A failed: %v", inst.InstanceID, addErr)
}
diffOutput, diffErr := dockerExecOutput(diffCtx, name, diffCmdArgs...)
```

- [ ] **Step 5: Run tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./cmd/tracker-swebench/... -v`
Expected: All tests PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/tracker-swebench/docker.go cmd/tracker-swebench/docker_test.go
git commit -m "fix(swebench): capture new files and staged changes in patch

Replace bare 'git diff' with 'git add -A && git diff HEAD' so that
untracked new files and staged changes are included in the patch.
Use a fresh context for diff capture so partial patches are preserved
even when the parent context is cancelled."
```

---

### Task 3: Secure API key passing with `--env-file`

**Criticality:** Critical — keys visible in `docker inspect` and `/proc`.

**Files:**
- Modify: `cmd/tracker-swebench/docker.go:42-48,99-113,116-130`
- Modify: `cmd/tracker-swebench/docker_test.go:72-100`

- [ ] **Step 1: Write test for new `writeEnvFile` helper**

Add to `docker_test.go`:

```go
func TestWriteEnvFile(t *testing.T) {
	env := map[string]string{
		"API_KEY": "sk-secret",
		"MODEL":   "claude-sonnet-4-6",
	}

	path, err := writeEnvFile(env)
	if err != nil {
		t.Fatalf("writeEnvFile: %v", err)
	}
	defer os.Remove(path)

	// File must exist and have restrictive permissions.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat env file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("env file permissions = %o, want 0600", info.Mode().Perm())
	}

	// Contents must be KEY=VALUE lines.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "API_KEY=sk-secret\n") {
		t.Errorf("expected API_KEY=sk-secret in env file, got:\n%s", content)
	}
	if !strings.Contains(content, "MODEL=claude-sonnet-4-6\n") {
		t.Errorf("expected MODEL line in env file, got:\n%s", content)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./cmd/tracker-swebench/ -run TestWriteEnvFile -v`
Expected: FAIL — `writeEnvFile` is undefined.

- [ ] **Step 3: Implement `writeEnvFile`**

Add to `docker.go` (replace `buildEnvFlags`):

```go
// writeEnvFile writes environment variables to a temporary file in KEY=VALUE
// format with mode 0600, returning the path. The caller must os.Remove the
// file when done. This avoids exposing secrets via docker -e flags which are
// visible in process listings and docker inspect output.
func writeEnvFile(env map[string]string) (string, error) {
	f, err := os.CreateTemp("", "swebench-env-*")
	if err != nil {
		return "", fmt.Errorf("create env file: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", fmt.Errorf("chmod env file: %w", err)
	}
	for k, v := range env {
		if _, err := fmt.Fprintf(f, "%s=%s\n", k, v); err != nil {
			f.Close()
			os.Remove(f.Name())
			return "", fmt.Errorf("write env var: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("close env file: %w", err)
	}
	return f.Name(), nil
}

// buildEnvFileFlag returns the docker --env-file flag pair for a given path.
func buildEnvFileFlag(envFilePath string) []string {
	return []string{"--env-file", envFilePath}
}
```

- [ ] **Step 4: Update `dockerExec` and `dockerExecCapture` to accept env file path**

Replace the old `buildEnvFlags` call pattern. In `dockerExec` and `dockerExecCapture`, change the `env map[string]string` parameter to `envFilePath string`. When `envFilePath != ""`, prepend `--env-file <path>` to exec args instead of `-e K=V` pairs.

Update `dockerExec`:

```go
func dockerExec(ctx context.Context, container string, envFilePath string, args ...string) error {
	execArgs := []string{"exec"}
	if envFilePath != "" {
		execArgs = append(execArgs, "--env-file", envFilePath)
	}
	execArgs = append(execArgs, container)
	execArgs = append(execArgs, args...)

	cmd := exec.CommandContext(ctx, "docker", execArgs...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker exec %s: %w\nstderr: %s", container, err, stderr.String())
	}
	return nil
}
```

Update `dockerExecCapture`:

```go
func dockerExecCapture(ctx context.Context, container string, envFilePath string, args ...string) (string, error) {
	execArgs := []string{"exec"}
	if envFilePath != "" {
		execArgs = append(execArgs, "--env-file", envFilePath)
	}
	execArgs = append(execArgs, container)
	execArgs = append(execArgs, args...)

	cmd := exec.CommandContext(ctx, "docker", execArgs...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("docker exec %s: %w\noutput: %s", container, err, out.String())
	}
	return out.String(), nil
}
```

- [ ] **Step 5: Update all call sites in `RunInstance`**

In `RunInstance`, write the env file once before the agent step and clean up in the defer:

```go
// Write agent env to a secure temp file (avoids key exposure in process args).
envFilePath, envErr := writeEnvFile(agentEnv)
if envErr != nil {
    return "", AgentSummary{}, fmt.Errorf("write env file: %w", envErr)
}
defer os.Remove(envFilePath)
```

Update all `dockerExec`/`dockerExecCapture` calls:
- Clone step: pass `""` (no env needed)
- pip install: pass `""` (no env needed)
- agent-runner: pass `envFilePath`
- git add/diff: pass `""` (no env needed)

- [ ] **Step 6: Update `TestBuildEnvFlags` to test new pattern**

Replace `TestBuildEnvFlags` in `docker_test.go` with a test for `buildEnvFileFlag`:

```go
func TestBuildEnvFileFlag(t *testing.T) {
	flags := buildEnvFileFlag("/tmp/env-abc123")
	if len(flags) != 2 {
		t.Fatalf("expected 2 flags, got %d: %v", len(flags), flags)
	}
	if flags[0] != "--env-file" {
		t.Errorf("flags[0] = %q, want \"--env-file\"", flags[0])
	}
	if flags[1] != "/tmp/env-abc123" {
		t.Errorf("flags[1] = %q, want path", flags[1])
	}
}
```

- [ ] **Step 7: Run tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./cmd/tracker-swebench/... -v`
Expected: All tests PASS.

- [ ] **Step 8: Commit**

```bash
git add cmd/tracker-swebench/docker.go cmd/tracker-swebench/docker_test.go
git commit -m "fix(swebench): secure API key passing with --env-file

Replace docker exec -e KEY=VAL flags with a temp --env-file at mode
0600. Keys are no longer visible in docker inspect, /proc/pid/cmdline,
or host process listings."
```

---

### Task 4: Add container resource limits and `--platform` to build

**Criticality:** Critical (resource limits) + Important (platform).

**Files:**
- Modify: `cmd/tracker-swebench/docker.go:147-152,173-178`
- Modify: `cmd/tracker-swebench/build.sh:10-13`

- [ ] **Step 1: Add resource limit fields to `DockerRunner`**

In `docker.go`, extend the `DockerRunner` struct:

```go
type DockerRunner struct {
	Image      string
	CacheDir   string
	Timeout    time.Duration
	MemoryMB   int // container memory limit in MB (0 = no limit)
	CPUs       float64 // container CPU limit (0 = no limit)
	PidsLimit  int // container PID limit (0 = no limit)
}
```

- [ ] **Step 2: Apply limits in `docker create`**

In `RunInstance`, update the `createArgs` block (around line 173):

```go
createArgs := []string{"create", "--name", name}
if r.CacheDir != "" {
    createArgs = append(createArgs, "-v", r.CacheDir+":/cache:ro")
}
if r.MemoryMB > 0 {
    createArgs = append(createArgs, "--memory", fmt.Sprintf("%dm", r.MemoryMB))
}
if r.CPUs > 0 {
    createArgs = append(createArgs, "--cpus", fmt.Sprintf("%.1f", r.CPUs))
}
if r.PidsLimit > 0 {
    createArgs = append(createArgs, "--pids-limit", fmt.Sprintf("%d", r.PidsLimit))
}
createArgs = append(createArgs, r.Image, "sleep", "infinity")
```

- [ ] **Step 3: Set defaults in `main.go`**

In `main.go`, update the DockerRunner construction (around line 89):

```go
docker := &DockerRunner{
    Image:    *dockerImage,
    CacheDir: cacheDir,
    Timeout:  *timeout,
    MemoryMB: 4096,  // 4 GB
    CPUs:     2.0,
    PidsLimit: 512,
}
```

- [ ] **Step 4: Fix `build.sh` platform flag**

In `build.sh`, add `--platform linux/amd64` to the docker build command:

```bash
echo "==> Cross-compiling agent-runner for linux/amd64..."
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o agent-runner ./agent-runner/

echo "==> Building Docker image: tracker-swebench-base..."
docker build --platform linux/amd64 -t tracker-swebench-base .
```

- [ ] **Step 5: Run build and tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go build ./cmd/tracker-swebench/ && go test ./cmd/tracker-swebench/... -v`
Expected: Build succeeds, all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/tracker-swebench/docker.go cmd/tracker-swebench/main.go cmd/tracker-swebench/build.sh
git commit -m "fix(swebench): add container resource limits and platform flag

Set --memory 4g, --cpus 2, --pids-limit 512 on docker create to
prevent runaway containers from exhausting host resources. Add
--platform linux/amd64 to docker build for consistent cross-platform
image builds."
```

---

### Task 5: Handle SIGTERM + container labels + orphan cleanup

**Criticality:** Critical — orphaned containers on SIGTERM/SIGKILL, no recovery mechanism.

**Files:**
- Modify: `cmd/tracker-swebench/docker.go:156-169,173`
- Modify: `cmd/tracker-swebench/main.go:113-115`

- [ ] **Step 1: Add SIGTERM to signal handler**

In `main.go`, update the signal handler (line 114):

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()
```

Add `"syscall"` to the imports.

- [ ] **Step 2: Add run label and pre-flight cleanup to `DockerRunner`**

Add a `RunLabel` field and a `CleanupStale` method to `DockerRunner` in `docker.go`:

```go
type DockerRunner struct {
	Image     string
	CacheDir  string
	Timeout   time.Duration
	MemoryMB  int
	CPUs      float64
	PidsLimit int
	RunLabel  string // label value for orphan cleanup (e.g., run timestamp)
}

// CleanupStale removes any containers with the swebench label from prior runs.
func (r *DockerRunner) CleanupStale(ctx context.Context) {
	cmd := exec.CommandContext(ctx, "docker", "ps", "-a",
		"--filter", "label=swebench",
		"--format", "{{.Names}}")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return // best-effort
	}
	for _, name := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if name == "" {
			continue
		}
		log.Printf("cleaning up stale container: %s", name)
		_ = dockerCmd(ctx, "rm", "-f", name)
	}
}
```

- [ ] **Step 3: Add label to `docker create`**

In `RunInstance`, add the label to `createArgs`:

```go
createArgs := []string{"create", "--name", name, "--label", "swebench=" + r.RunLabel}
```

- [ ] **Step 4: Set RunLabel and call CleanupStale in `main.go`**

In `main.go`, set the label and clean up before the loop:

```go
docker := &DockerRunner{
    Image:    *dockerImage,
    CacheDir: cacheDir,
    Timeout:  *timeout,
    MemoryMB: 4096,
    CPUs:     2.0,
    PidsLimit: 512,
    RunLabel:  time.Now().Format("20060102-150405"),
}

// Clean up orphaned containers from prior crashed runs.
docker.CleanupStale(ctx)
```

- [ ] **Step 5: Run tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go build ./cmd/tracker-swebench/ && go test ./cmd/tracker-swebench/... -v`
Expected: Build succeeds, all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/tracker-swebench/docker.go cmd/tracker-swebench/main.go
git commit -m "fix(swebench): handle SIGTERM and clean up orphaned containers

Add SIGTERM to signal handler so CI runners and Docker orchestrators
trigger graceful shutdown. Label containers with swebench=<timestamp>
and clean up stale containers from prior crashed runs on startup."
```

---

### Task 6: Fix container naming and instance ID validation

**Criticality:** Important — name collisions on concurrent runs, path traversal via instance ID.

**Files:**
- Modify: `cmd/tracker-swebench/docker.go:24-27`
- Modify: `cmd/tracker-swebench/docker_test.go:10-15`
- Modify: `cmd/tracker-swebench/dataset.go` (add validation)
- Modify: `cmd/tracker-swebench/dataset_test.go`

- [ ] **Step 1: Write test for instance ID validation**

Add to `dataset_test.go`:

```go
func TestValidateInstanceID(t *testing.T) {
	tests := []struct {
		id    string
		valid bool
	}{
		{"django__django-11099", true},
		{"sympy__sympy-12345", true},
		{"scikit-learn__scikit-learn-9876", true},
		{"", false},
		{"../../etc/passwd", false},
		{"foo;rm -rf /", false},
		{"foo bar", false},
		{"a/b", false},
	}
	for _, tt := range tests {
		err := validateInstanceID(tt.id)
		if tt.valid && err != nil {
			t.Errorf("validateInstanceID(%q) = %v, want nil", tt.id, err)
		}
		if !tt.valid && err == nil {
			t.Errorf("validateInstanceID(%q) = nil, want error", tt.id)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./cmd/tracker-swebench/ -run TestValidateInstanceID -v`
Expected: FAIL — `validateInstanceID` is undefined.

- [ ] **Step 3: Implement validation**

Add to `dataset.go`:

```go
import "regexp"

// instanceIDPattern matches valid SWE-bench instance IDs: alphanumeric, underscores, hyphens, dots.
var instanceIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.=-]*$`)

// validateInstanceID checks that an instance ID is safe for use as a Docker
// container name suffix and filesystem path component.
func validateInstanceID(id string) error {
	if id == "" {
		return fmt.Errorf("empty instance ID")
	}
	if !instanceIDPattern.MatchString(id) {
		return fmt.Errorf("invalid instance ID %q: must match [a-zA-Z0-9][a-zA-Z0-9_.=-]*", id)
	}
	return nil
}
```

- [ ] **Step 4: Call validation in `LoadDataset`**

In `dataset.go`, add validation after unmarshaling each instance (around line 61):

```go
if err := json.Unmarshal([]byte(line), &inst); err != nil {
    return nil, fmt.Errorf("line %d: %w", lineNum, err)
}
if err := validateInstanceID(inst.InstanceID); err != nil {
    return nil, fmt.Errorf("line %d: %w", lineNum, err)
}
```

- [ ] **Step 5: Update `containerName` to include run prefix**

In `docker.go`, update `containerName` to accept a run prefix:

```go
func containerName(runLabel, instanceID string) string {
	return "swe-" + runLabel + "-" + instanceID
}
```

Update the `TestContainerName` test and all call sites in `RunInstance` (pass `r.RunLabel`).

- [ ] **Step 6: Run tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./cmd/tracker-swebench/... -v`
Expected: All tests PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/tracker-swebench/dataset.go cmd/tracker-swebench/dataset_test.go cmd/tracker-swebench/docker.go cmd/tracker-swebench/docker_test.go
git commit -m "fix(swebench): validate instance IDs and scope container names

Add regex validation on instance IDs to prevent path traversal and
shell metacharacter injection. Include run label in container names
to prevent collisions across concurrent runs."
```

---

### Task 7: Fix empty-patch resume behavior

**Criticality:** Critical — timed-out instances permanently skipped on resume.

**Files:**
- Modify: `cmd/tracker-swebench/results.go:21-27,63-79`
- Modify: `cmd/tracker-swebench/results_test.go`
- Modify: `cmd/tracker-swebench/main.go:158-160`

- [ ] **Step 1: Write test for empty-patch not marking completed**

Add to `results_test.go`:

```go
func TestResultsWriter_EmptyPatchNotCompleted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "predictions.jsonl")

	w, err := NewResultsWriter(path, "test-model")
	if err != nil {
		t.Fatalf("NewResultsWriter: %v", err)
	}

	// Write empty patch — should still write the line but NOT mark as completed.
	if err := w.WritePrediction("instance-timeout", ""); err != nil {
		t.Fatalf("WritePrediction: %v", err)
	}

	// Instance should NOT be in the completed set.
	if w.IsCompleted("instance-timeout") {
		t.Error("empty-patch instance should not be marked as completed")
	}

	// Write a real patch — should mark as completed.
	if err := w.WritePrediction("instance-ok", "diff --git a/fix.py"); err != nil {
		t.Fatalf("WritePrediction: %v", err)
	}
	if !w.IsCompleted("instance-ok") {
		t.Error("non-empty patch instance should be marked as completed")
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Resume: empty-patch instance should NOT be skipped.
	w2, err := NewResultsWriter(path, "test-model")
	if err != nil {
		t.Fatalf("NewResultsWriter (resume): %v", err)
	}
	defer w2.Close()

	if w2.IsCompleted("instance-timeout") {
		t.Error("resume: empty-patch instance should not be marked completed")
	}
	if !w2.IsCompleted("instance-ok") {
		t.Error("resume: non-empty patch instance should be completed")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./cmd/tracker-swebench/ -run TestResultsWriter_EmptyPatchNotCompleted -v`
Expected: FAIL — empty-patch instance is currently marked completed.

- [ ] **Step 3: Fix `WritePrediction` to only mark completed on non-empty patch**

In `results.go`, update `WritePrediction` (line 64-79):

```go
func (w *ResultsWriter) WritePrediction(instanceID, patch string) error {
	p := Prediction{
		InstanceID:      instanceID,
		ModelNameOrPath: w.model,
		ModelPatch:      patch,
	}
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal prediction: %w", err)
	}
	data = append(data, '\n')
	if _, err := w.file.Write(data); err != nil {
		return fmt.Errorf("write prediction: %w", err)
	}
	// Only mark as completed if the patch is non-empty. Empty patches from
	// timeouts or errors should be retried on resume.
	if patch != "" {
		w.completed[instanceID] = struct{}{}
	}
	return nil
}
```

- [ ] **Step 4: Fix resume reader to also skip empty patches**

In `NewResultsWriter`, update the resume-read logic (line 42):

```go
if jsonErr := json.Unmarshal([]byte(line), &p); jsonErr == nil && p.InstanceID != "" && p.ModelPatch != "" {
    completed[p.InstanceID] = struct{}{}
}
```

- [ ] **Step 5: Update existing test expectations**

The existing `TestResultsWriter_WriteAndResume` writes `"diff1"` and `"diff2"` (non-empty), so it should still pass. The integration test writes `"diff --git a/fix.py"` (non-empty), also fine.

- [ ] **Step 6: Run tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./cmd/tracker-swebench/... -v`
Expected: All tests PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/tracker-swebench/results.go cmd/tracker-swebench/results_test.go
git commit -m "fix(swebench): don't mark empty-patch predictions as completed

Only add instances to the completed set when the patch is non-empty.
Timed-out or errored instances that produced no patch will be retried
on resume instead of permanently skipped."
```

---

### Task 8: Fix exit codes, error counting, timeout detection, and WritePrediction handling

**Criticality:** Critical (exit codes) + Important (timeout, write errors).

**Files:**
- Modify: `cmd/tracker-swebench/results.go:103-113,116-147`
- Modify: `cmd/tracker-swebench/results_test.go:104-127`
- Modify: `cmd/tracker-swebench/main.go:92,144,159-187,190`

- [ ] **Step 1: Add `Errors` field to `RunStats` and update `Summary`**

In `results.go`, add the field and update `Summary`:

```go
type RunStats struct {
	Total        int
	Completed    int
	Skipped      int
	Errors       int
	TimedOut     int
	Patched      int
	InputTokens  int64
	OutputTokens int64
	StartTime    time.Time
}
```

Update `Summary()` to include the Errors line:

```go
func (s *RunStats) Summary() string {
	elapsed := time.Since(s.StartTime).Round(time.Second)

	patchPct := 0.0
	if s.Completed > 0 {
		patchPct = float64(s.Patched) / float64(s.Completed) * 100
	}
	completedPct := 0.0
	if s.Total > 0 {
		completedPct = float64(s.Completed) / float64(s.Total) * 100
	}

	inM := float64(s.InputTokens) / 1e6
	outM := float64(s.OutputTokens) / 1e6

	return fmt.Sprintf(
		"Run complete — elapsed: %s\n"+
			"  Total:     %d\n"+
			"  Completed: %d (%.1f%%)\n"+
			"  Skipped:   %d\n"+
			"  Errors:    %d\n"+
			"  Timed out: %d\n"+
			"  Patched:   %d (%.1f%% of completed)\n"+
			"  Tokens:    %.2fM in / %.2fM out",
		elapsed,
		s.Total,
		s.Completed, completedPct,
		s.Skipped,
		s.Errors,
		s.TimedOut,
		s.Patched, patchPct,
		inM, outM,
	)
}
```

- [ ] **Step 2: Fix timeout detection with `errors.Is`**

In `main.go`, replace the timeout string matching (line 181):

```go
if runErr != nil && strings.Contains(runErr.Error(), "context deadline exceeded") {
    stats.TimedOut++
}
```

With:

```go
if runErr != nil {
    stats.Errors++
    if errors.Is(runErr, context.DeadlineExceeded) {
        stats.TimedOut++
    }
}
```

Add `"errors"` to imports (and remove `"strings"` if no longer needed — check first).

- [ ] **Step 3: Fix WritePrediction error handling**

In `main.go`, make `WritePrediction` failure stop incrementing `Completed` (around line 159):

```go
if writeErr := rw.WritePrediction(inst.InstanceID, patch); writeErr != nil {
    log.Printf("[%s] write prediction: %v", inst.InstanceID, writeErr)
    stats.Errors++
    continue // skip stats update — this instance is not reliably recorded
}
```

Move `stats.Completed++` and the rest of the stats update after the write check:

```go
// Update stats only after successful prediction write.
stats.Completed++
stats.InputTokens += summary.InputTokens
stats.OutputTokens += summary.OutputTokens
if patch != "" {
    stats.Patched++
}
if runErr != nil {
    stats.Errors++
    if errors.Is(runErr, context.DeadlineExceeded) {
        stats.TimedOut++
    }
}
```

- [ ] **Step 4: Exit non-zero when errors occurred**

At the end of `main()` (line 190), replace:

```go
fmt.Println(stats.Summary())
```

With:

```go
fmt.Println(stats.Summary())
if stats.Errors > 0 {
    os.Exit(1)
}
```

- [ ] **Step 5: Update `TestRunStats_Summary`**

In `results_test.go`, add `Errors` to the test struct and check for it in the output:

```go
func TestRunStats_Summary(t *testing.T) {
	stats := RunStats{
		Total:        10,
		Completed:    7,
		Skipped:      2,
		Errors:       1,
		TimedOut:     1,
		Patched:      5,
		InputTokens:  1_500_000,
		OutputTokens: 300_000,
		StartTime:    time.Now().Add(-5 * time.Minute),
	}

	summary := stats.Summary()
	if summary == "" {
		t.Fatal("Summary returned empty string")
	}
	if !strings.Contains(summary, "Errors:    1") {
		t.Error("summary should contain Errors count")
	}
}
```

- [ ] **Step 6: Run tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./cmd/tracker-swebench/... -v`
Expected: All tests PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/tracker-swebench/main.go cmd/tracker-swebench/results.go cmd/tracker-swebench/results_test.go
git commit -m "fix(swebench): add error counting, fix exit codes and timeout detection

Add Errors field to RunStats and exit non-zero when any instance
errors. Replace string-matching timeout detection with errors.Is
for correctness. Stop counting instances as Completed when
WritePrediction fails."
```

---

### Task 9: Fix `int`/`int64` type mismatch and provider routing

**Criticality:** Important — latent overflow + silent misconfiguration.

**Files:**
- Modify: `cmd/tracker-swebench/agent-runner/main.go:85-90`
- Modify: `cmd/tracker-swebench/agent-runner/providers.go:10-25`
- Modify: `cmd/tracker-swebench/agent-runner/main_test.go`

- [ ] **Step 1: Fix type mismatch in `agentSummary`**

In `agent-runner/main.go`, align token types to `int64`:

```go
type agentSummary struct {
	Turns        int   `json:"turns"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	DurationMs   int64 `json:"duration_ms"`
}
```

Update the assignment block (around line 136-139) — `result.Usage.InputTokens` is `int`, so cast:

```go
if result.Turns > 0 {
    summary.Turns = result.Turns
    summary.InputTokens = int64(result.Usage.InputTokens)
    summary.OutputTokens = int64(result.Usage.OutputTokens)
}
```

- [ ] **Step 2: Fix `enc.Encode` error handling**

In `agent-runner/main.go`, around line 141-142:

```go
enc := json.NewEncoder(os.Stdout)
if encErr := enc.Encode(summary); encErr != nil {
    log.Printf("failed to encode summary: %v", encErr)
}
```

- [ ] **Step 3: Add provider validation in `buildLLMClient`**

In `agent-runner/main.go`, validate the provider before building the client. Replace the `buildLLMClient` function (line 150-173):

```go
func buildLLMClient(provider, baseURL string) (*llm.Client, error) {
	// Validate provider upfront.
	switch provider {
	case "anthropic", "openai":
		// supported
	default:
		return nil, fmt.Errorf("unsupported provider %q: must be \"anthropic\" or \"openai\"", provider)
	}

	constructors := map[string]func(string) (llm.ProviderAdapter, error){
		provider: func(key string) (llm.ProviderAdapter, error) {
			switch provider {
			case "openai":
				return newOpenAIAdapter(key, baseURL)
			default:
				return newAnthropicAdapter(key, baseURL)
			}
		},
	}

	client, err := llm.NewClientFromEnv(constructors)
	if err != nil {
		return nil, err
	}

	client.AddMiddleware(llm.NewRetryMiddleware(
		llm.WithMaxRetries(3),
		llm.WithBaseDelay(2*time.Second),
	))

	return client, nil
}
```

- [ ] **Step 4: Add provider validation test**

Add to `agent-runner/main_test.go`:

```go
func TestBuildLLMClient_UnsupportedProvider(t *testing.T) {
	_, err := buildLLMClient("gemini", "")
	if err == nil {
		t.Fatal("expected error for unsupported provider, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported provider") {
		t.Errorf("expected 'unsupported provider' in error, got: %v", err)
	}
}
```

Add `"strings"` to the test file imports.

- [ ] **Step 5: Run tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./cmd/tracker-swebench/... -v`
Expected: All tests PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/tracker-swebench/agent-runner/main.go cmd/tracker-swebench/agent-runner/main_test.go
git commit -m "fix(swebench): align token types to int64 and validate provider

Change agentSummary token fields from int to int64 to match the
harness-side AgentSummary struct. Handle enc.Encode error. Reject
unsupported providers with a clear error instead of silently falling
through to the Anthropic adapter."
```

---

### Task 10: Improve system prompt for SWE-bench

**Criticality:** Important — missing key tactics that affect benchmark scores.

**Files:**
- Modify: `cmd/tracker-swebench/agent-runner/main.go:19-39`

- [ ] **Step 1: Update the system prompt**

Replace `swebenchSystemPrompt` in `agent-runner/main.go`:

```go
const swebenchSystemPrompt = `You are an expert software engineer tasked with fixing a GitHub issue.

You have access to the repository at /workspace. The repository is already
checked out at the correct commit.

## Your task
Fix the issue described below. Make the minimal changes necessary to resolve
the issue. Do not refactor unrelated code.

## Approach
1. Read the issue carefully. Understand what's broken and what the expected behavior is.
2. Reproduce the bug first. Find and run the relevant test(s) to confirm the failure.
3. Check git log --oneline -10 for recent context around the affected code.
4. Explore the relevant code. Use grep_search and glob to find the right files.
5. Write a fix. Make targeted edits — smallest diff that solves the problem.
6. Run the failing test again to verify your fix resolves it.
7. Run the broader test suite to check for regressions.

## Rules
- Do NOT create new test files. The evaluation uses the repo's existing test suite.
- Do NOT modify test files unless the issue specifically requires it.
- Keep your changes minimal and focused.
- If you're unsure about the fix, read more code before editing.
- Always re-read a file before editing it if you haven't read it recently.
- After editing, verify the fix by running the relevant tests.
- You may use absolute paths in bash commands (e.g., /workspace/tests/). Only file
  tool arguments (read_file, write_file, etc.) require relative paths.`
```

- [ ] **Step 2: Increase default timeout to 30 minutes**

In `agent-runner/main.go`, update the default timeout (line 59):

```go
Timeout:  30 * time.Minute,
```

Also update the default in `main.go` (the outer harness, line 28):

```go
timeout := flag.Duration("timeout", 30*time.Minute, "per-instance timeout")
```

- [ ] **Step 3: Run tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./cmd/tracker-swebench/... -v`
Expected: All tests PASS. (The `TestParseConfig_Defaults` test checks for `10*time.Minute` — update it to `30*time.Minute`.)

- [ ] **Step 4: Commit**

```bash
git add cmd/tracker-swebench/agent-runner/main.go cmd/tracker-swebench/agent-runner/main_test.go cmd/tracker-swebench/main.go
git commit -m "fix(swebench): improve system prompt and increase default timeout

Add reproduce-before-fix, verify-after-fix, and git-log instructions
to the agent system prompt. Clarify that absolute paths are allowed
in bash commands. Increase default timeout from 10 to 30 minutes to
match community baselines for complex SWE-bench instances."
```

---

### Task 11: Add `flag.Usage`, populate `RunMeta.Commit`

**Criticality:** Critical (no usage) + Important (reproducibility gap).

**Files:**
- Modify: `cmd/tracker-swebench/main.go:19-31,68-78`

- [ ] **Step 1: Add custom `flag.Usage`**

In `main.go`, add a custom usage function before `flag.Parse()`:

```go
flag.Usage = func() {
    fmt.Fprintf(os.Stderr, `tracker-swebench — run tracker's code agent against SWE-bench Lite instances

Usage:
  tracker-swebench --dataset <path> [flags]

Prerequisites:
  1. Build the Docker image:  cd cmd/tracker-swebench && bash build.sh
  2. Set API key:             export ANTHROPIC_API_KEY=sk-ant-...
  3. Download dataset:        SWE-bench Lite JSONL from the SWE-bench repository

Examples:
  tracker-swebench --dataset swebench_lite.jsonl
  tracker-swebench --dataset swebench_lite.jsonl --instance django__django-11099
  tracker-swebench --dataset swebench_lite.jsonl --model gpt-5.2 --provider openai
  tracker-swebench --dataset swebench_lite.jsonl --force --timeout 30m

Flags:
`)
    flag.PrintDefaults()
}
```

- [ ] **Step 2: Populate `RunMeta.Commit` from build info**

Add to `main.go`:

```go
import "runtime/debug"

// buildCommit returns the VCS revision from Go build info, or "unknown".
func buildCommit() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			if len(s.Value) > 12 {
				return s.Value[:12]
			}
			return s.Value
		}
	}
	return "unknown"
}
```

Then in `main()`, set the commit field (around line 68):

```go
meta := RunMeta{
    Model:      *model,
    Provider:   *provider,
    GatewayURL: *gatewayURL,
    Dataset:    *dataset,
    MaxTurns:   *maxTurns,
    Timeout:    timeout.String(),
    Commit:     buildCommit(),
}
```

- [ ] **Step 3: Run tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go build ./cmd/tracker-swebench/ && go test ./cmd/tracker-swebench/... -v`
Expected: Build succeeds, all tests PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/tracker-swebench/main.go
git commit -m "fix(swebench): add flag.Usage with prerequisites and populate RunMeta.Commit

Add custom flag.Usage that documents prerequisites (Docker image,
API keys, dataset), shows example invocations, and lists all flags.
Populate RunMeta.Commit from Go VCS build metadata for reproducibility."
```

---

### Task 12: Add `claude-sonnet-4-6` to model catalog

**Criticality:** Important — cost reporting is always $0 for the default model.

**Files:**
- Modify: `llm/catalog.go:36-48`

- [ ] **Step 1: Write failing test**

The existing pricing test in `llm/pricing.go` already references `claude-sonnet-4-6`. Add a catalog test:

```go
// In a new or existing test file for catalog:
func TestGetModelInfo_Sonnet46(t *testing.T) {
	info := GetModelInfo("claude-sonnet-4-6")
	if info == nil {
		t.Fatal("claude-sonnet-4-6 not found in catalog")
	}
	if info.Provider != "anthropic" {
		t.Errorf("Provider = %q, want \"anthropic\"", info.Provider)
	}
	if info.ContextWindow != 200000 {
		t.Errorf("ContextWindow = %d, want 200000", info.ContextWindow)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./llm/ -run TestGetModelInfo_Sonnet46 -v`
Expected: FAIL — `claude-sonnet-4-6` not found.

- [ ] **Step 3: Add catalog entry**

In `llm/catalog.go`, add after the `claude-sonnet-4-5` entry (after line 48):

```go
{
    ID:                "claude-sonnet-4-6",
    Provider:          "anthropic",
    DisplayName:       "Claude Sonnet 4.6",
    ContextWindow:     200000,
    MaxOutput:         16000,
    SupportsTools:     true,
    SupportsVision:    true,
    SupportsReasoning: true,
    InputCostPerM:     3.0,
    OutputCostPerM:    15.0,
    Aliases:           []string{"sonnet-4-6"},
},
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./llm/... -v`
Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add llm/catalog.go
git commit -m "fix(llm): add claude-sonnet-4-6 to model catalog

The model was in pricing.go but missing from catalog.go, causing
GetModelInfo to return nil and cost reporting to show $0.00 for
the swebench harness default model."
```

---

## Self-Review Checklist

**Spec coverage:**
- [x] Critical #1 (shell injection) → Task 1
- [x] Critical #2 (git diff) → Task 2
- [x] Critical #3 (--reference path) → Task 1 step 4
- [x] Critical #4 (empty-patch resume) → Task 7
- [x] Critical #5 (API key exposure) → Task 3
- [x] Critical #6 (exit codes) → Task 8
- [x] Critical #7 (resource limits) → Task 4
- [x] Critical #8 (orphaned containers) → Task 5
- [x] Critical #9 (no docs/usage) → Task 11
- [x] Important #10 (catalog) → Task 12
- [x] Important #16 (path traversal) → Task 6
- [x] Important #17 (name collisions) → Task 6
- [x] Important #18 (WritePrediction) → Task 8
- [x] Important #19 (timeout detection) → Task 8
- [x] Important #20 (int/int64) → Task 9
- [x] Important #21 (timeout too short) → Task 10
- [x] Important #22 (provider routing) → Task 9
- [x] Important #28 (system prompt) → Task 10

**Deferred to follow-up (not blocking ship):**
- Important #11 (absolute path prohibition) — partially addressed in Task 10 system prompt
- Important #12 (environment_setup_commit) — requires per-instance conda, large scope change
- Important #13 (Python 3.11 only) — requires multi-image strategy, separate design
- Important #14,15 (root, network isolation) — separate hardening pass
- Important #23 (ARG_MAX) — partially mitigated by --env-file in Task 3
- Important #24 (arm64) — build.sh fix in Task 4 adds `--platform`; full multi-arch is follow-up
- Important #25 (RunMeta.Commit) → Task 11
- Important #29 (compaction) — tuning, not a code fix

**Placeholder scan:** No TBDs, TODOs, or "implement later" found.

**Type consistency:** `buildCloneCommands` signature used consistently in Tasks 1 and 6. `containerName` updated signature propagated to all call sites. `AgentSummary`/`agentSummary` aligned to `int64` in Task 9.
