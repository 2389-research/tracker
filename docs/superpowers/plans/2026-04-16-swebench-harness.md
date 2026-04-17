# SWE-bench Harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go harness (`cmd/tracker-swebench/`) that runs tracker's agent against SWE-bench Lite instances in Docker containers, producing `predictions.jsonl` for the official evaluator.

**Architecture:** Two binaries — an orchestrator on the host manages Docker containers and collects patches; an agent-runner inside each container creates an `agent.Session` and runs it against the repo. Sequential execution, append-only output for resumability.

**Tech Stack:** Go, Docker CLI (shelled out), tracker `agent` + `llm` packages, SWE-bench Lite JSONL dataset.

**Spec:** `docs/superpowers/specs/2026-04-16-swebench-harness-design.md`

---

## File Structure

```
cmd/tracker-swebench/
  main.go              — orchestrator entry point, CLI flags, run loop
  dataset.go           — JSONL parsing, Instance struct
  dataset_test.go      — dataset parsing tests
  docker.go            — Docker container lifecycle (create, exec, rm)
  docker_test.go       — Docker helper tests (unit-testable parts)
  results.go           — predictions.jsonl writing, resumability, run summary
  results_test.go      — results/resumability tests
  Dockerfile           — base image with agent-runner baked in
  agent-runner/
    main.go            — in-container agent binary
    main_test.go       — agent-runner config parsing tests
```

---

### Task 1: Dataset Parsing

Parse SWE-bench Lite JSONL into Go structs.

**Files:**
- Create: `cmd/tracker-swebench/dataset.go`
- Create: `cmd/tracker-swebench/dataset_test.go`

- [ ] **Step 1: Write the failing test for Instance struct and LoadDataset**

```go
// cmd/tracker-swebench/dataset_test.go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDataset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")
	data := `{"instance_id":"django__django-11099","repo":"django/django","base_commit":"abc123","problem_statement":"Fix the bug","hints_text":"Check models.py","version":"3.0","environment_setup_commit":"def456"}
{"instance_id":"requests__requests-1234","repo":"psf/requests","base_commit":"789abc","problem_statement":"Handle timeout","hints_text":"","version":"2.25","environment_setup_commit":"789abc"}
`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	instances, err := LoadDataset(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(instances))
	}
	if instances[0].InstanceID != "django__django-11099" {
		t.Errorf("instance_id = %q, want django__django-11099", instances[0].InstanceID)
	}
	if instances[0].Repo != "django/django" {
		t.Errorf("repo = %q, want django/django", instances[0].Repo)
	}
	if instances[0].BaseCommit != "abc123" {
		t.Errorf("base_commit = %q, want abc123", instances[0].BaseCommit)
	}
	if instances[0].ProblemStatement != "Fix the bug" {
		t.Errorf("problem_statement = %q", instances[0].ProblemStatement)
	}
	if instances[0].HintsText != "Check models.py" {
		t.Errorf("hints_text = %q", instances[0].HintsText)
	}
	if instances[0].Version != "3.0" {
		t.Errorf("version = %q", instances[0].Version)
	}
	if instances[0].EnvSetupCommit != "def456" {
		t.Errorf("environment_setup_commit = %q", instances[0].EnvSetupCommit)
	}
}

func TestLoadDataset_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	instances, err := LoadDataset(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 0 {
		t.Errorf("expected 0 instances, got %d", len(instances))
	}
}

func TestLoadDataset_BadJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(path, []byte("not json\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadDataset(path)
	if err == nil {
		t.Fatal("expected error for bad JSON")
	}
}

func TestInstance_AgentPrompt(t *testing.T) {
	inst := Instance{
		ProblemStatement: "Fix the bug in models.py",
		HintsText:        "Check the QuerySet class",
	}
	prompt := inst.AgentPrompt()
	if prompt == "" {
		t.Fatal("prompt should not be empty")
	}
	if !contains(prompt, "Fix the bug in models.py") {
		t.Error("prompt should contain problem statement")
	}
	if !contains(prompt, "Check the QuerySet class") {
		t.Error("prompt should contain hints")
	}

	// Without hints
	inst2 := Instance{ProblemStatement: "Fix it"}
	prompt2 := inst2.AgentPrompt()
	if contains(prompt2, "Hints") {
		t.Error("prompt without hints should not mention hints section")
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && stringContains(s, substr)
}

func stringContains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/tracker-swebench/ -run TestLoadDataset -v`
Expected: FAIL — `LoadDataset` not defined

- [ ] **Step 3: Implement dataset.go**

```go
// cmd/tracker-swebench/dataset.go

// ABOUTME: SWE-bench dataset JSONL parsing into Go structs.
// ABOUTME: Handles Instance loading and prompt construction for the agent runner.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Instance represents a single SWE-bench evaluation instance.
type Instance struct {
	InstanceID       string `json:"instance_id"`
	Repo             string `json:"repo"`
	BaseCommit       string `json:"base_commit"`
	ProblemStatement string `json:"problem_statement"`
	HintsText        string `json:"hints_text"`
	Version          string `json:"version"`
	EnvSetupCommit   string `json:"environment_setup_commit"`
}

// RepoURL returns the GitHub clone URL for this instance's repo.
func (inst Instance) RepoURL() string {
	return "https://github.com/" + inst.Repo + ".git"
}

// AgentPrompt constructs the user input for the agent session.
func (inst Instance) AgentPrompt() string {
	var b strings.Builder
	b.WriteString(inst.ProblemStatement)
	if inst.HintsText != "" {
		b.WriteString("\n\n## Hints\n\n")
		b.WriteString(inst.HintsText)
	}
	return b.String()
}

// LoadDataset reads a SWE-bench JSONL file and returns all instances.
func LoadDataset(path string) ([]Instance, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open dataset: %w", err)
	}
	defer f.Close()

	var instances []Instance
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024) // 10MB line buffer
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var inst Instance
		if err := json.Unmarshal([]byte(line), &inst); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNum, err)
		}
		instances = append(instances, inst)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan dataset: %w", err)
	}
	return instances, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/tracker-swebench/ -run "TestLoadDataset|TestInstance" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/tracker-swebench/dataset.go cmd/tracker-swebench/dataset_test.go
git commit -m "feat(swebench): add dataset JSONL parsing"
```

---

### Task 2: Results Writer and Resumability

Write predictions.jsonl in append mode, track completed instances, print progress and summary.

**Files:**
- Create: `cmd/tracker-swebench/results.go`
- Create: `cmd/tracker-swebench/results_test.go`

- [ ] **Step 1: Write failing tests**

```go
// cmd/tracker-swebench/results_test.go
package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResultsWriter_WriteAndResume(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "predictions.jsonl")

	// Write two predictions.
	w, err := NewResultsWriter(path, "claude-sonnet-4-6")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WritePrediction("inst-1", "diff --git a/foo.py\n+fix"); err != nil {
		t.Fatal(err)
	}
	if err := w.WritePrediction("inst-2", ""); err != nil {
		t.Fatal(err)
	}
	w.Close()

	// Re-open and check completed set.
	w2, err := NewResultsWriter(path, "claude-sonnet-4-6")
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	if !w2.IsCompleted("inst-1") {
		t.Error("inst-1 should be completed")
	}
	if !w2.IsCompleted("inst-2") {
		t.Error("inst-2 should be completed")
	}
	if w2.IsCompleted("inst-3") {
		t.Error("inst-3 should not be completed")
	}
	if w2.CompletedCount() != 2 {
		t.Errorf("completed count = %d, want 2", w2.CompletedCount())
	}
}

func TestRunStats_Summary(t *testing.T) {
	stats := &RunStats{
		Total:     10,
		Completed: 7,
		Skipped:   1,
		TimedOut:  2,
		Patched:   6,
		StartTime: time.Now().Add(-5 * time.Minute),
	}
	summary := stats.Summary()
	if summary == "" {
		t.Fatal("summary should not be empty")
	}
}

func TestWriteRunMeta(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run_meta.json")
	meta := RunMeta{
		Model:      "claude-sonnet-4-6",
		Provider:   "anthropic",
		GatewayURL: "https://example.com",
		Dataset:    "swebench_lite",
		MaxTurns:   50,
		Timeout:    "10m",
	}
	if err := WriteRunMeta(path, meta); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("run_meta.json should not be empty")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/tracker-swebench/ -run "TestResultsWriter|TestRunStats|TestWriteRunMeta" -v`
Expected: FAIL

- [ ] **Step 3: Implement results.go**

```go
// cmd/tracker-swebench/results.go

// ABOUTME: Prediction output writer with append-mode resumability.
// ABOUTME: Tracks completed instances and produces run summaries.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Prediction is one line of predictions.jsonl.
type Prediction struct {
	InstanceID      string `json:"instance_id"`
	ModelNameOrPath string `json:"model_name_or_path"`
	ModelPatch      string `json:"model_patch"`
}

// ResultsWriter manages append-only predictions.jsonl with resumability.
type ResultsWriter struct {
	file      *os.File
	model     string
	completed map[string]bool
}

// NewResultsWriter opens (or creates) the predictions file and loads
// already-completed instance IDs for resume support.
func NewResultsWriter(path, model string) (*ResultsWriter, error) {
	completed := make(map[string]bool)

	// Read existing predictions for resumability.
	if f, err := os.Open(path); err == nil {
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			var p Prediction
			if json.Unmarshal(scanner.Bytes(), &p) == nil && p.InstanceID != "" {
				completed[p.InstanceID] = true
			}
		}
		f.Close()
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open predictions file: %w", err)
	}

	return &ResultsWriter{file: file, model: model, completed: completed}, nil
}

// WritePrediction appends one prediction and marks the instance as completed.
func (w *ResultsWriter) WritePrediction(instanceID, patch string) error {
	p := Prediction{
		InstanceID:      instanceID,
		ModelNameOrPath: w.model,
		ModelPatch:      patch,
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if _, err := w.file.Write(append(data, '\n')); err != nil {
		return err
	}
	w.completed[instanceID] = true
	return nil
}

// IsCompleted reports whether an instance has already been processed.
func (w *ResultsWriter) IsCompleted(instanceID string) bool {
	return w.completed[instanceID]
}

// CompletedCount returns how many instances have been processed.
func (w *ResultsWriter) CompletedCount() int {
	return len(w.completed)
}

// Close closes the underlying file.
func (w *ResultsWriter) Close() error {
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}

// RunStats tracks aggregate stats for the run.
type RunStats struct {
	Total       int
	Completed   int
	Skipped     int
	TimedOut    int
	Patched     int
	InputTokens int64
	OutputTokens int64
	StartTime   time.Time
}

// Summary returns a human-readable run summary.
func (s *RunStats) Summary() string {
	elapsed := time.Since(s.StartTime).Round(time.Second)
	var b strings.Builder
	fmt.Fprintf(&b, "\n--- Run Summary ---\n")
	fmt.Fprintf(&b, "Completed: %d/%d\n", s.Completed, s.Total)
	if s.Skipped > 0 {
		fmt.Fprintf(&b, "Skipped (setup failure): %d\n", s.Skipped)
	}
	if s.TimedOut > 0 {
		fmt.Fprintf(&b, "Timed out: %d\n", s.TimedOut)
	}
	pct := float64(0)
	if s.Total > 0 {
		pct = float64(s.Patched) / float64(s.Total) * 100
	}
	fmt.Fprintf(&b, "Patches produced: %d/%d (%.1f%%)\n", s.Patched, s.Total, pct)
	fmt.Fprintf(&b, "Total tokens: %.1fM input / %.1fM output\n",
		float64(s.InputTokens)/1e6, float64(s.OutputTokens)/1e6)
	fmt.Fprintf(&b, "Elapsed: %s\n", elapsed)
	return b.String()
}

// RunMeta stores metadata about a benchmark run.
type RunMeta struct {
	Model      string `json:"model"`
	Provider   string `json:"provider"`
	GatewayURL string `json:"gateway_url,omitempty"`
	Dataset    string `json:"dataset"`
	MaxTurns   int    `json:"max_turns"`
	Timeout    string `json:"timeout"`
	StartedAt  string `json:"started_at"`
	Commit     string `json:"commit,omitempty"`
}

// WriteRunMeta writes the run metadata to a JSON file.
func WriteRunMeta(path string, meta RunMeta) error {
	if meta.StartedAt == "" {
		meta.StartedAt = time.Now().UTC().Format(time.RFC3339)
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/tracker-swebench/ -run "TestResultsWriter|TestRunStats|TestWriteRunMeta" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/tracker-swebench/results.go cmd/tracker-swebench/results_test.go
git commit -m "feat(swebench): add results writer with resumability"
```

---

### Task 3: Docker Container Lifecycle

Shell out to `docker` CLI for container create/start/exec/stop/rm.

**Files:**
- Create: `cmd/tracker-swebench/docker.go`
- Create: `cmd/tracker-swebench/docker_test.go`

- [ ] **Step 1: Write failing tests for Docker helpers**

The Docker functions shell out to `docker`, so unit tests cover argument construction and output parsing. Integration tests (actually running Docker) are gated behind a build tag.

```go
// cmd/tracker-swebench/docker_test.go
package main

import (
	"strings"
	"testing"
)

func TestContainerName(t *testing.T) {
	name := containerName("django__django-11099")
	if name != "swe-django__django-11099" {
		t.Errorf("container name = %q, want swe-django__django-11099", name)
	}
}

func TestBuildCloneCmd(t *testing.T) {
	cmd := buildCloneCmd("https://github.com/django/django.git", "abc123", "/workspace", "/cache/django__django")
	joined := strings.Join(cmd, " ")
	if !strings.Contains(joined, "--reference") {
		t.Error("expected --reference flag for cached clone")
	}
	if !strings.Contains(joined, "/cache/django__django") {
		t.Error("expected cache path in command")
	}
}

func TestBuildCloneCmd_NoCache(t *testing.T) {
	cmd := buildCloneCmd("https://github.com/django/django.git", "abc123", "/workspace", "")
	joined := strings.Join(cmd, " ")
	if strings.Contains(joined, "--reference") {
		t.Error("should not have --reference without cache")
	}
}

func TestBuildEnvFlags(t *testing.T) {
	env := map[string]string{
		"SWEBENCH_MODEL":    "claude-sonnet-4-6",
		"SWEBENCH_PROVIDER": "anthropic",
	}
	flags := buildEnvFlags(env)
	if len(flags) != 4 {
		t.Errorf("expected 4 flags (-e key=val pairs), got %d", len(flags))
	}
}

func TestParseDiffOutput(t *testing.T) {
	raw := "diff --git a/foo.py b/foo.py\n--- a/foo.py\n+++ b/foo.py\n@@ -1 +1 @@\n-old\n+new\n"
	patch := parseDiffOutput(raw)
	if patch == "" {
		t.Fatal("patch should not be empty")
	}
	lines := strings.Count(patch, "\n")
	if lines == 0 {
		t.Error("patch should have lines")
	}
}

func TestParseDiffOutput_Empty(t *testing.T) {
	patch := parseDiffOutput("")
	if patch != "" {
		t.Errorf("empty diff should produce empty patch, got %q", patch)
	}
}

func TestPatchLineCount(t *testing.T) {
	patch := "line1\nline2\nline3\n"
	count := patchLineCount(patch)
	if count != 3 {
		t.Errorf("expected 3 lines, got %d", count)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/tracker-swebench/ -run "TestContainerName|TestBuildCloneCmd|TestBuildEnvFlags|TestParseDiffOutput|TestPatchLineCount" -v`
Expected: FAIL

- [ ] **Step 3: Implement docker.go**

```go
// cmd/tracker-swebench/docker.go

// ABOUTME: Docker container lifecycle management for SWE-bench instances.
// ABOUTME: Shells out to docker CLI for create, exec, stop, and rm operations.
package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

// containerName returns the Docker container name for a SWE-bench instance.
func containerName(instanceID string) string {
	return "swe-" + instanceID
}

// buildCloneCmd constructs the git clone + checkout command sequence.
func buildCloneCmd(repoURL, commit, workDir, cachePath string) []string {
	var parts []string
	if cachePath != "" {
		parts = append(parts, "git", "clone", "--reference", cachePath, repoURL, workDir)
	} else {
		parts = append(parts, "git", "clone", repoURL, workDir)
	}
	parts = append(parts, "&&", "git", "-C", workDir, "checkout", commit)
	return []string{"sh", "-c", strings.Join(parts, " ")}
}

// buildEnvFlags constructs -e KEY=VAL flags for docker exec.
func buildEnvFlags(env map[string]string) []string {
	var flags []string
	for k, v := range env {
		flags = append(flags, "-e", k+"="+v)
	}
	return flags
}

// parseDiffOutput trims and returns the git diff output.
func parseDiffOutput(raw string) string {
	return strings.TrimSpace(raw)
}

// patchLineCount returns the number of non-empty lines in a patch.
func patchLineCount(patch string) int {
	if patch == "" {
		return 0
	}
	lines := strings.Split(strings.TrimRight(patch, "\n"), "\n")
	return len(lines)
}

// DockerRunner manages the Docker container lifecycle for one instance.
type DockerRunner struct {
	Image      string
	CacheDir   string // host-side bare clone cache
	Timeout    time.Duration
}

// RunInstance executes the full container lifecycle for one SWE-bench instance.
// Returns the git diff patch and an AgentSummary parsed from the agent-runner output.
func (d *DockerRunner) RunInstance(ctx context.Context, inst Instance, agentEnv map[string]string) (patch string, summary AgentSummary, err error) {
	name := containerName(inst.InstanceID)

	// Determine cache path for this repo.
	repoKey := strings.ReplaceAll(inst.Repo, "/", "__")
	cachePath := ""
	if d.CacheDir != "" {
		cachePath = "/cache/" + repoKey
	}

	// Create + start container with cache mount.
	createArgs := []string{"create", "--name", name}
	if d.CacheDir != "" {
		createArgs = append(createArgs, "-v", d.CacheDir+":/cache:ro")
	}
	createArgs = append(createArgs, d.Image, "sleep", "infinity")

	if err := dockerCmd(ctx, createArgs...); err != nil {
		return "", AgentSummary{}, fmt.Errorf("docker create: %w", err)
	}
	defer func() {
		// Always clean up.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = dockerCmd(cleanupCtx, "stop", name)
		_ = dockerCmd(cleanupCtx, "rm", "-f", name)
	}()

	if err := dockerCmd(ctx, "start", name); err != nil {
		return "", AgentSummary{}, fmt.Errorf("docker start: %w", err)
	}

	// Clone repo + checkout.
	cloneCmd := buildCloneCmd(inst.RepoURL(), inst.BaseCommit, "/workspace", cachePath)
	if err := dockerExec(ctx, name, nil, cloneCmd...); err != nil {
		return "", AgentSummary{}, fmt.Errorf("git clone: %w", err)
	}

	// Install dependencies.
	installCmd := []string{"sh", "-c", "cd /workspace && pip install -e . 2>&1 | tail -5"}
	if err := dockerExec(ctx, name, nil, installCmd...); err != nil {
		log.Printf("[%s] pip install failed (continuing): %v", inst.InstanceID, err)
	}

	// Run the agent.
	agentCtx, agentCancel := context.WithTimeout(ctx, d.Timeout)
	defer agentCancel()

	agentOutput, agentErr := dockerExecCapture(agentCtx, name, agentEnv, "agent-runner")
	summary = parseAgentSummary(agentOutput)

	if agentErr != nil {
		if agentCtx.Err() != nil {
			return "", summary, fmt.Errorf("agent timeout: %w", agentCtx.Err())
		}
		// Agent failed but may have produced partial changes.
		log.Printf("[%s] agent error (capturing partial diff): %v", inst.InstanceID, agentErr)
	}

	// Capture git diff.
	diffOutput, diffErr := dockerExecOutput(ctx, name, "git", "-C", "/workspace", "diff")
	if diffErr != nil {
		return "", summary, fmt.Errorf("git diff: %w", diffErr)
	}

	return parseDiffOutput(diffOutput), summary, nil
}

// dockerCmd runs a docker command and returns any error.
func dockerCmd(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, stderr.String())
	}
	return nil
}

// dockerExec runs a command inside a container via docker exec.
func dockerExec(ctx context.Context, container string, env map[string]string, args ...string) error {
	execArgs := []string{"exec"}
	execArgs = append(execArgs, buildEnvFlags(env)...)
	execArgs = append(execArgs, container)
	execArgs = append(execArgs, args...)
	return dockerCmd(ctx, execArgs...)
}

// dockerExecCapture runs a command inside a container and returns combined output.
func dockerExecCapture(ctx context.Context, container string, env map[string]string, args ...string) (string, error) {
	execArgs := []string{"exec"}
	execArgs = append(execArgs, buildEnvFlags(env)...)
	execArgs = append(execArgs, container)
	execArgs = append(execArgs, args...)

	cmd := exec.CommandContext(ctx, "docker", execArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String() + stderr.String(), err
}

// dockerExecOutput runs a command inside a container and returns stdout.
func dockerExecOutput(ctx context.Context, container string, args ...string) (string, error) {
	execArgs := append([]string{"exec", container}, args...)
	cmd := exec.CommandContext(ctx, "docker", execArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, stderr.String())
	}
	return stdout.String(), nil
}

// AgentSummary is the JSON summary the agent-runner writes as its last stdout line.
type AgentSummary struct {
	Turns        int   `json:"turns"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	DurationMs   int64 `json:"duration_ms"`
}

// parseAgentSummary extracts the JSON summary from the agent-runner output.
// The summary is the last non-empty line of output.
func parseAgentSummary(output string) AgentSummary {
	var summary AgentSummary
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if err := json.Unmarshal([]byte(line), &summary); err == nil && summary.Turns > 0 {
			return summary
		}
		break // only try last non-empty line
	}
	return summary
}
```

Note: need to add `"encoding/json"` to the imports — it's used by `parseAgentSummary`. The full import block should be:

```go
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/tracker-swebench/ -run "TestContainerName|TestBuildCloneCmd|TestBuildEnvFlags|TestParseDiffOutput|TestPatchLineCount" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/tracker-swebench/docker.go cmd/tracker-swebench/docker_test.go
git commit -m "feat(swebench): add Docker container lifecycle management"
```

---

### Task 4: Agent Runner Binary

The minimal binary that runs inside Docker containers.

**Files:**
- Create: `cmd/tracker-swebench/agent-runner/main.go`
- Create: `cmd/tracker-swebench/agent-runner/main_test.go`

- [ ] **Step 1: Write failing tests for config parsing**

```go
// cmd/tracker-swebench/agent-runner/main_test.go
package main

import (
	"testing"
	"time"
)

func TestParseConfig_Defaults(t *testing.T) {
	cfg := parseConfig()
	if cfg.RepoDir != "/workspace" {
		t.Errorf("default repo dir = %q, want /workspace", cfg.RepoDir)
	}
	if cfg.MaxTurns != 50 {
		t.Errorf("default max turns = %d, want 50", cfg.MaxTurns)
	}
	if cfg.Timeout != 10*time.Minute {
		t.Errorf("default timeout = %v, want 10m", cfg.Timeout)
	}
}

func TestParseConfig_FromEnv(t *testing.T) {
	t.Setenv("SWEBENCH_REPO_DIR", "/repo")
	t.Setenv("SWEBENCH_MODEL", "gpt-5.4")
	t.Setenv("SWEBENCH_PROVIDER", "openai")
	t.Setenv("SWEBENCH_MAX_TURNS", "30")
	t.Setenv("SWEBENCH_TIMEOUT", "5m")

	cfg := parseConfig()
	if cfg.RepoDir != "/repo" {
		t.Errorf("repo dir = %q, want /repo", cfg.RepoDir)
	}
	if cfg.Model != "gpt-5.4" {
		t.Errorf("model = %q, want gpt-5.4", cfg.Model)
	}
	if cfg.Provider != "openai" {
		t.Errorf("provider = %q, want openai", cfg.Provider)
	}
	if cfg.MaxTurns != 30 {
		t.Errorf("max turns = %d, want 30", cfg.MaxTurns)
	}
	if cfg.Timeout != 5*time.Minute {
		t.Errorf("timeout = %v, want 5m", cfg.Timeout)
	}
}

func TestSystemPrompt(t *testing.T) {
	prompt := swebenchSystemPrompt
	if prompt == "" {
		t.Fatal("system prompt should not be empty")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/tracker-swebench/agent-runner/ -run "TestParseConfig|TestSystemPrompt" -v`
Expected: FAIL

- [ ] **Step 3: Implement agent-runner main.go**

```go
// cmd/tracker-swebench/agent-runner/main.go

// ABOUTME: In-container agent binary for SWE-bench benchmarking.
// ABOUTME: Creates an agent.Session directly (Layer 2 only) and runs it against a repo.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/2389-research/tracker"
	"github.com/2389-research/tracker/agent"
	agentexec "github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/llm"
)

const swebenchSystemPrompt = `You are an expert software engineer tasked with fixing a GitHub issue.

You have access to the repository at /workspace. The repository is already
checked out at the correct commit.

## Your task
Fix the issue described below. Make the minimal changes necessary to resolve
the issue. Do not refactor unrelated code.

## Approach
1. Read the issue carefully. Understand what's broken and what the expected behavior is.
2. Explore the relevant code. Use grep_search and glob to find the right files.
3. Write a fix. Make targeted edits — smallest diff that solves the problem.
4. Run the existing test suite to verify your fix doesn't break anything.
5. If there are specific test commands mentioned in the issue, run those.

## Rules
- Do NOT create new test files. The evaluation uses the repo's existing test suite.
- Do NOT modify test files unless the issue specifically requires it.
- Keep your changes minimal and focused.
- If you're unsure about the fix, read more code before editing.`

type runnerConfig struct {
	Instance string
	RepoDir  string
	Model    string
	Provider string
	MaxTurns int
	Timeout  time.Duration
}

func parseConfig() runnerConfig {
	cfg := runnerConfig{
		Instance: os.Getenv("SWEBENCH_INSTANCE"),
		RepoDir:  "/workspace",
		Model:    "claude-sonnet-4-6",
		Provider: "anthropic",
		MaxTurns: 50,
		Timeout:  10 * time.Minute,
	}
	if v := os.Getenv("SWEBENCH_REPO_DIR"); v != "" {
		cfg.RepoDir = v
	}
	if v := os.Getenv("SWEBENCH_MODEL"); v != "" {
		cfg.Model = v
	}
	if v := os.Getenv("SWEBENCH_PROVIDER"); v != "" {
		cfg.Provider = v
	}
	if v := os.Getenv("SWEBENCH_MAX_TURNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxTurns = n
		}
	}
	if v := os.Getenv("SWEBENCH_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Timeout = d
		}
	}
	return cfg
}

type agentSummary struct {
	Turns        int   `json:"turns"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	DurationMs   int64 `json:"duration_ms"`
}

func main() {
	cfg := parseConfig()
	if cfg.Instance == "" {
		log.Fatal("SWEBENCH_INSTANCE is required")
	}

	// Build LLM client using tracker's provider resolution (handles TRACKER_GATEWAY_URL).
	baseURL := tracker.ResolveProviderBaseURL(cfg.Provider)
	client, err := buildLLMClient(cfg.Provider, baseURL)
	if err != nil {
		log.Fatalf("failed to create LLM client: %v", err)
	}
	defer client.Close()

	// Configure the agent session.
	sessionCfg := agent.DefaultConfig()
	sessionCfg.Model = cfg.Model
	sessionCfg.Provider = cfg.Provider
	sessionCfg.MaxTurns = cfg.MaxTurns
	sessionCfg.CommandTimeout = 30 * time.Second
	sessionCfg.MaxCommandTimeout = 5 * time.Minute
	sessionCfg.ContextCompaction = agent.CompactionAuto
	sessionCfg.CompactionThreshold = 0.7
	sessionCfg.ReflectOnError = true
	sessionCfg.WorkingDir = cfg.RepoDir
	sessionCfg.SystemPrompt = swebenchSystemPrompt

	env := agentexec.NewLocalEnvironment(cfg.RepoDir)
	sess, err := agent.NewSession(client, sessionCfg, agent.WithEnvironment(env))
	if err != nil {
		log.Fatalf("failed to create session: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	start := time.Now()
	result, runErr := sess.Run(ctx, cfg.Instance)
	elapsed := time.Since(start)

	// Write summary as last line of stdout.
	summary := agentSummary{
		Turns:        result.Turns,
		InputTokens:  int64(result.Usage.InputTokens),
		OutputTokens: int64(result.Usage.OutputTokens),
		DurationMs:   elapsed.Milliseconds(),
	}
	data, _ := json.Marshal(summary)
	fmt.Println(string(data))

	if runErr != nil {
		log.Printf("agent error: %v", runErr)
		os.Exit(1)
	}
}

// buildLLMClient creates an LLM client for a single provider with optional base URL.
func buildLLMClient(provider, baseURL string) (*llm.Client, error) {
	constructors := buildConstructors(provider, baseURL)
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

// buildConstructors returns a single-provider constructor map.
// The agent-runner only needs one provider at a time.
func buildConstructors(provider, baseURL string) map[string]func(string) (llm.ProviderAdapter, error) {
	// Import the provider adapter packages. These are determined at compile time.
	// We use the same pattern as tracker's allProviderConstructors.
	return map[string]func(string) (llm.ProviderAdapter, error){
		provider: func(key string) (llm.ProviderAdapter, error) {
			return newProviderAdapter(provider, key, baseURL)
		},
	}
}

// newProviderAdapter creates the appropriate adapter for a provider.
func newProviderAdapter(provider, key, baseURL string) (llm.ProviderAdapter, error) {
	switch provider {
	case "anthropic":
		return newAnthropicAdapter(key, baseURL)
	case "openai":
		return newOpenAIAdapter(key, baseURL)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", provider)
	}
}
```

Note: The `newAnthropicAdapter` and `newOpenAIAdapter` helper functions need to be implemented in a separate file to keep things clean. Add a `providers.go` in the agent-runner package:

```go
// cmd/tracker-swebench/agent-runner/providers.go

// ABOUTME: Provider adapter constructors for the SWE-bench agent runner.
// ABOUTME: Wraps Anthropic and OpenAI adapters with optional base URL override.
package main

import (
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/llm/anthropic"
	"github.com/2389-research/tracker/llm/openai"
)

func newAnthropicAdapter(key, baseURL string) (llm.ProviderAdapter, error) {
	var opts []anthropic.Option
	if baseURL != "" {
		opts = append(opts, anthropic.WithBaseURL(baseURL))
	}
	return anthropic.New(key, opts...), nil
}

func newOpenAIAdapter(key, baseURL string) (llm.ProviderAdapter, error) {
	var opts []openai.Option
	if baseURL != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}
	return openai.New(key, opts...), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/tracker-swebench/agent-runner/ -run "TestParseConfig|TestSystemPrompt" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/tracker-swebench/agent-runner/
git commit -m "feat(swebench): add agent-runner binary for in-container execution"
```

---

### Task 5: Dockerfile

Build the base Docker image with agent-runner baked in.

**Files:**
- Create: `cmd/tracker-swebench/Dockerfile`

- [ ] **Step 1: Create the Dockerfile**

```dockerfile
# cmd/tracker-swebench/Dockerfile

# ABOUTME: Base Docker image for SWE-bench instance containers.
# ABOUTME: Contains the agent-runner binary and common system dependencies.

FROM python:3.11-bookworm

RUN apt-get update && apt-get install -y \
    git \
    build-essential \
    curl \
    wget \
    && rm -rf /var/lib/apt/lists/*

# Pre-install commonly needed Python packages.
RUN pip install --no-cache-dir setuptools wheel

COPY agent-runner /usr/local/bin/agent-runner

WORKDIR /workspace
```

- [ ] **Step 2: Verify agent-runner cross-compiles**

Run: `GOOS=linux GOARCH=amd64 go build -o /tmp/agent-runner ./cmd/tracker-swebench/agent-runner/`
Expected: produces `/tmp/agent-runner` binary for linux/amd64

- [ ] **Step 3: Commit**

```bash
git add cmd/tracker-swebench/Dockerfile
git commit -m "feat(swebench): add Dockerfile for base image"
```

---

### Task 6: Orchestrator Binary

The main CLI that ties everything together: flags, run loop, progress output.

**Files:**
- Create: `cmd/tracker-swebench/main.go`

- [ ] **Step 1: Implement the orchestrator**

```go
// cmd/tracker-swebench/main.go

// ABOUTME: SWE-bench orchestrator — runs tracker's agent against SWE-bench Lite instances.
// ABOUTME: Manages Docker containers, collects patches, writes predictions.jsonl.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	// CLI flags.
	dataset := flag.String("dataset", "", "Path to SWE-bench Lite JSONL file (required)")
	model := flag.String("model", "claude-sonnet-4-6", "Model name")
	provider := flag.String("provider", "anthropic", "Provider name (anthropic, openai)")
	gatewayURL := flag.String("gateway-url", "", "Cloudflare AI Gateway URL")
	output := flag.String("output", "./predictions.jsonl", "Output predictions file")
	resultsDir := flag.String("results-dir", "./results", "Results directory for logs and metadata")
	maxTurns := flag.Int("max-turns", 50, "Agent turn ceiling per instance")
	timeout := flag.Duration("timeout", 10*time.Minute, "Wall-clock timeout per instance")
	instance := flag.String("instance", "", "Run single instance by ID (for debugging)")
	force := flag.Bool("force", false, "Re-run already-completed instances")
	dockerImage := flag.String("docker-image", "tracker-swebench-base", "Base Docker image name")
	flag.Parse()

	if *dataset == "" {
		fmt.Fprintln(os.Stderr, "error: --dataset is required")
		flag.Usage()
		os.Exit(1)
	}

	// Load dataset.
	instances, err := LoadDataset(*dataset)
	if err != nil {
		log.Fatalf("failed to load dataset: %v", err)
	}
	log.Printf("loaded %d instances from %s", len(instances), *dataset)

	// Filter to single instance if specified.
	if *instance != "" {
		var filtered []Instance
		for _, inst := range instances {
			if inst.InstanceID == *instance {
				filtered = append(filtered, inst)
			}
		}
		if len(filtered) == 0 {
			log.Fatalf("instance %q not found in dataset", *instance)
		}
		instances = filtered
	}

	// Set up results directory.
	logsDir := filepath.Join(*resultsDir, "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		log.Fatalf("failed to create logs dir: %v", err)
	}
	cacheDir := filepath.Join(*resultsDir, "repo-cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		log.Fatalf("failed to create cache dir: %v", err)
	}

	// Write run metadata.
	meta := RunMeta{
		Model:      *model,
		Provider:   *provider,
		GatewayURL: *gatewayURL,
		Dataset:    *dataset,
		MaxTurns:   *maxTurns,
		Timeout:    timeout.String(),
	}
	metaPath := filepath.Join(*resultsDir, "run_meta.json")
	if err := WriteRunMeta(metaPath, meta); err != nil {
		log.Fatalf("failed to write run_meta.json: %v", err)
	}

	// Open results writer.
	writer, err := NewResultsWriter(*output, *model)
	if err != nil {
		log.Fatalf("failed to open results writer: %v", err)
	}
	defer writer.Close()

	// Set up Docker runner.
	docker := &DockerRunner{
		Image:    *dockerImage,
		CacheDir: cacheDir,
		Timeout:  *timeout,
	}

	// Build agent environment variables.
	agentEnv := map[string]string{
		"SWEBENCH_MODEL":    *model,
		"SWEBENCH_PROVIDER": *provider,
		"SWEBENCH_MAX_TURNS": fmt.Sprintf("%d", *maxTurns),
		"SWEBENCH_TIMEOUT":  timeout.String(),
	}
	if *gatewayURL != "" {
		agentEnv["TRACKER_GATEWAY_URL"] = *gatewayURL
	}
	// Pass through API keys from host environment.
	for _, key := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY"} {
		if v := os.Getenv(key); v != "" {
			agentEnv[key] = v
		}
	}

	// Handle Ctrl+C gracefully.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Run loop.
	stats := &RunStats{
		Total:     len(instances),
		StartTime: time.Now(),
	}

	for i, inst := range instances {
		if ctx.Err() != nil {
			log.Println("interrupted — stopping")
			break
		}

		// Skip completed instances unless --force.
		if !*force && writer.IsCompleted(inst.InstanceID) {
			log.Printf("[%d/%d] %s ... skipped (already completed)", i+1, len(instances), inst.InstanceID)
			continue
		}

		// Set instance-specific env.
		env := make(map[string]string)
		for k, v := range agentEnv {
			env[k] = v
		}
		env["SWEBENCH_INSTANCE"] = inst.AgentPrompt()

		log.Printf("[%d/%d] %s ...", i+1, len(instances), inst.InstanceID)

		// Ensure bare clone cache exists for this repo.
		repoKey := strings.ReplaceAll(inst.Repo, "/", "__")
		barePath := filepath.Join(cacheDir, repoKey)
		if err := ensureBareClone(ctx, inst.RepoURL(), barePath); err != nil {
			log.Printf("[%d/%d] %s ... setup failure: %v", i+1, len(instances), inst.InstanceID, err)
			stats.Skipped++
			continue
		}

		// Run the instance.
		patch, summary, runErr := docker.RunInstance(ctx, inst, env)

		// Write prediction regardless of error (may have partial patch).
		if writeErr := writer.WritePrediction(inst.InstanceID, patch); writeErr != nil {
			log.Printf("failed to write prediction: %v", writeErr)
		}

		// Log per-instance output.
		logPath := filepath.Join(logsDir, inst.InstanceID+".log")
		if logErr := os.WriteFile(logPath, []byte(fmt.Sprintf("patch_lines=%d turns=%d err=%v\n", patchLineCount(patch), summary.Turns, runErr)), 0644); logErr != nil {
			log.Printf("failed to write log: %v", logErr)
		}

		// Update stats.
		stats.Completed++
		stats.InputTokens += summary.InputTokens
		stats.OutputTokens += summary.OutputTokens
		if patch != "" {
			stats.Patched++
		}

		if runErr != nil {
			if strings.Contains(runErr.Error(), "timeout") {
				stats.TimedOut++
				log.Printf("[%d/%d] %s ... %d turns, timeout (no patch)", i+1, len(instances), inst.InstanceID, summary.Turns)
			} else {
				log.Printf("[%d/%d] %s ... error: %v", i+1, len(instances), inst.InstanceID, runErr)
			}
		} else {
			durationSec := float64(summary.DurationMs) / 1000
			log.Printf("[%d/%d] %s ... %d turns, %.1fs, patch: %d lines",
				i+1, len(instances), inst.InstanceID, summary.Turns, durationSec, patchLineCount(patch))
		}
	}

	fmt.Print(stats.Summary())
}

// ensureBareClone creates a bare git clone if it doesn't already exist.
func ensureBareClone(ctx context.Context, repoURL, path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil // already cached
	}
	log.Printf("caching bare clone: %s -> %s", repoURL, path)
	cmd := execCommand(ctx, "git", "clone", "--bare", repoURL, path)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone --bare: %w: %s", err, stderr.String())
	}
	return nil
}

// execCommand creates an exec.Cmd (extracted for testability).
var execCommand = execCommandImpl

func execCommandImpl(ctx context.Context, name string, args ...string) *execCmd {
	cmd := exec.CommandContext(ctx, name, args...)
	return &execCmd{cmd}
}

// execCmd wraps exec.Cmd to allow mocking in tests.
type execCmd struct {
	*exec.Cmd
}
```

Note: The `execCommand` abstraction at the bottom is unnecessary complexity — remove it and use `exec.CommandContext` directly. The `ensureBareClone` function should use the same approach as the rest of the file. Corrected implementation for `ensureBareClone`:

```go
// ensureBareClone creates a bare git clone if it doesn't already exist.
func ensureBareClone(ctx context.Context, repoURL, path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil // already cached
	}
	log.Printf("caching bare clone: %s -> %s", repoURL, path)
	cmd := exec.CommandContext(ctx, "git", "clone", "--bare", repoURL, path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone --bare: %w: %s", err, stderr.String())
	}
	return nil
}
```

Remove the `execCommand`, `execCommandImpl`, and `execCmd` types entirely. Add `"bytes"` and `"os/exec"` to the imports.

Full import block:

```go
import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
)
```

- [ ] **Step 2: Verify the orchestrator compiles**

Run: `go build ./cmd/tracker-swebench/`
Expected: compiles without errors

- [ ] **Step 3: Commit**

```bash
git add cmd/tracker-swebench/main.go
git commit -m "feat(swebench): add orchestrator binary with run loop"
```

---

### Task 7: Integration Test — Single Instance Dry Run

Test the full flow end-to-end with a single mock instance (no real LLM calls).

**Files:**
- Create: `cmd/tracker-swebench/integration_test.go`

- [ ] **Step 1: Write integration test for the dataset → results pipeline**

This tests everything except Docker and LLM calls: dataset loading, results writing, resumability, and run metadata.

```go
// cmd/tracker-swebench/integration_test.go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFullPipeline_DatasetToResults(t *testing.T) {
	dir := t.TempDir()

	// Create a small test dataset.
	datasetPath := filepath.Join(dir, "test.jsonl")
	dataset := `{"instance_id":"test__repo-001","repo":"test/repo","base_commit":"abc","problem_statement":"Fix bug","hints_text":"","version":"1.0","environment_setup_commit":"abc"}
{"instance_id":"test__repo-002","repo":"test/repo","base_commit":"def","problem_statement":"Add feature","hints_text":"Look at utils.py","version":"1.0","environment_setup_commit":"def"}
`
	if err := os.WriteFile(datasetPath, []byte(dataset), 0644); err != nil {
		t.Fatal(err)
	}

	// Load dataset.
	instances, err := LoadDataset(datasetPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(instances))
	}

	// Verify AgentPrompt includes hints when present.
	prompt1 := instances[0].AgentPrompt()
	if prompt1 != "Fix bug" {
		t.Errorf("prompt without hints = %q", prompt1)
	}
	prompt2 := instances[1].AgentPrompt()
	if !strings.Contains(prompt2, "Look at utils.py") {
		t.Error("prompt with hints should include hints")
	}

	// Write predictions.
	predPath := filepath.Join(dir, "predictions.jsonl")
	w, err := NewResultsWriter(predPath, "test-model")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WritePrediction("test__repo-001", "diff --git a/x.py\n+fix"); err != nil {
		t.Fatal(err)
	}
	w.Close()

	// Resume — should see first instance as completed.
	w2, err := NewResultsWriter(predPath, "test-model")
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	if !w2.IsCompleted("test__repo-001") {
		t.Error("inst-001 should be completed after resume")
	}
	if w2.IsCompleted("test__repo-002") {
		t.Error("inst-002 should not be completed")
	}

	// Write metadata.
	metaPath := filepath.Join(dir, "run_meta.json")
	if err := WriteRunMeta(metaPath, RunMeta{Model: "test-model", Dataset: "test"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(metaPath)
	if len(data) == 0 {
		t.Fatal("run_meta.json is empty")
	}
}
```

Note: needs `"strings"` import added.

- [ ] **Step 2: Run the integration test**

Run: `go test ./cmd/tracker-swebench/ -run TestFullPipeline -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add cmd/tracker-swebench/integration_test.go
git commit -m "test(swebench): add integration test for dataset-to-results pipeline"
```

---

### Task 8: Build Script and Final Wiring

Add a build script that cross-compiles agent-runner and builds the Docker image.

**Files:**
- Create: `cmd/tracker-swebench/build.sh`

- [ ] **Step 1: Create the build script**

```bash
#!/usr/bin/env bash
# cmd/tracker-swebench/build.sh
# ABOUTME: Builds the agent-runner binary and Docker image for SWE-bench.
# ABOUTME: Cross-compiles for linux/amd64 and bakes into tracker-swebench-base image.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

echo "==> Cross-compiling agent-runner for linux/amd64..."
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o agent-runner ./agent-runner/

echo "==> Building Docker image: tracker-swebench-base..."
docker build -t tracker-swebench-base .

echo "==> Cleaning up agent-runner binary..."
rm -f agent-runner

echo "==> Done. Image: tracker-swebench-base"
echo "    Run: go build -o tracker-swebench . && ./tracker-swebench --dataset <path> --model <model>"
```

- [ ] **Step 2: Make it executable and verify the orchestrator builds**

```bash
chmod +x cmd/tracker-swebench/build.sh
go build ./cmd/tracker-swebench/
go build ./cmd/tracker-swebench/agent-runner/
```

Expected: both binaries compile without errors

- [ ] **Step 3: Run the full test suite**

Run: `go test ./... -short`
Expected: all packages pass including the new `cmd/tracker-swebench/` and `cmd/tracker-swebench/agent-runner/` packages

- [ ] **Step 4: Commit**

```bash
git add cmd/tracker-swebench/build.sh
git commit -m "feat(swebench): add build script for Docker image"
```

---

### Task 9: Final Verification and Cleanup

- [ ] **Step 1: Run full test suite**

Run: `go test ./... -short`
Expected: all packages pass

- [ ] **Step 2: Run go vet**

Run: `go vet ./...`
Expected: no issues

- [ ] **Step 3: Verify both binaries compile for linux/amd64**

```bash
GOOS=linux GOARCH=amd64 go build -o /dev/null ./cmd/tracker-swebench/
GOOS=linux GOARCH=amd64 go build -o /dev/null ./cmd/tracker-swebench/agent-runner/
```

Expected: both compile cleanly

- [ ] **Step 4: Final commit with any cleanup**

```bash
git add -A
git commit -m "chore(swebench): final cleanup and verification"
```
