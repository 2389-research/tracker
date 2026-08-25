// ABOUTME: Unit tests for Docker container lifecycle helpers in the swebench harness.
// ABOUTME: Tests cover helper functions only — no actual Docker daemon is required.
package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestContainerName(t *testing.T) {
	got := containerName("20260416-120000", "django__django-11095")
	want := "swe-20260416-120000-django__django-11095"
	if got != want {
		t.Errorf("containerName() = %q, want %q", got, want)
	}
}

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

func TestParseDiffOutput(t *testing.T) {
	raw := "  diff --git a/foo.py b/foo.py\n+added line\n-removed line\n  "
	got := parseDiffOutput(raw)
	want := "diff --git a/foo.py b/foo.py\n+added line\n-removed line"
	if got != want {
		t.Errorf("parseDiffOutput() = %q, want %q", got, want)
	}
}

func TestParseDiffOutput_Empty(t *testing.T) {
	got := parseDiffOutput("   ")
	if got != "" {
		t.Errorf("parseDiffOutput(whitespace) = %q, want \"\"", got)
	}
}

func TestPatchLineCount(t *testing.T) {
	patch := "diff --git a/foo.py b/foo.py\n+added\n-removed\n"
	got := patchLineCount(patch)
	// 3 non-empty lines
	if got != 3 {
		t.Errorf("patchLineCount() = %d, want 3", got)
	}
}

func TestPatchLineCount_Empty(t *testing.T) {
	got := patchLineCount("")
	if got != 0 {
		t.Errorf("patchLineCount(\"\") = %d, want 0", got)
	}
}

func TestParseAgentSummary(t *testing.T) {
	output := "some log line\nanother line\n{\"turns\":5,\"input_tokens\":1000,\"output_tokens\":200,\"duration_ms\":3500,\"termination_reason\":\"explicit_finish\",\"final_message\":\"done\",\"last_tool_calls\":[\"glob\",\"read\"]}\n"
	got := parseAgentSummary(output)
	if got.Turns != 5 {
		t.Errorf("Turns = %d, want 5", got.Turns)
	}
	if got.InputTokens != 1000 {
		t.Errorf("InputTokens = %d, want 1000", got.InputTokens)
	}
	if got.OutputTokens != 200 {
		t.Errorf("OutputTokens = %d, want 200", got.OutputTokens)
	}
	if got.DurationMs != 3500 {
		t.Errorf("DurationMs = %d, want 3500", got.DurationMs)
	}
	if got.TerminationReason != "explicit_finish" {
		t.Errorf("TerminationReason = %q, want explicit_finish", got.TerminationReason)
	}
	if got.FinalMessage != "done" {
		t.Errorf("FinalMessage = %q, want done", got.FinalMessage)
	}
	if len(got.LastToolCalls) != 2 || got.LastToolCalls[0] != "glob" || got.LastToolCalls[1] != "read" {
		t.Errorf("LastToolCalls = %#v, want [glob read]", got.LastToolCalls)
	}
}

func TestParseAgentSummary_NoJSON(t *testing.T) {
	output := "some log line\nanother log line\nplain text ending"
	got := parseAgentSummary(output)
	if got.Turns != 0 || got.InputTokens != 0 || got.OutputTokens != 0 || got.DurationMs != 0 {
		t.Errorf("expected zero-value AgentSummary for non-JSON output, got %+v", got)
	}
}

func TestParseAgentSummary_JSONBeforeLogTail(t *testing.T) {
	output := "{\"turns\":3,\"termination_reason\":\"tool_error\"}\n2026/04/21 00:00:00 agent session failed: boom"
	got := parseAgentSummary(output)
	if got.Turns != 3 {
		t.Errorf("Turns = %d, want 3", got.Turns)
	}
	if got.TerminationReason != "tool_error" {
		t.Errorf("TerminationReason = %q, want tool_error", got.TerminationReason)
	}
}

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

// --- #598: ownership-aware cleanup -------------------------------------------

func TestSelectContainersForCleanup_SkipsConcurrentLiveContainer(t *testing.T) {
	now := time.Now()
	staleTTL := time.Hour
	// Owner "host:b" belongs to a concurrent, still-live harness.
	alive := func(owner string) bool { return owner == "host:b" }

	infos := []containerCleanupInfo{
		// Another live harness's actively running container — MUST be preserved.
		{Name: "swe-runB-inst", Running: true, Owner: "host:b", CreatedAt: now.Add(-2 * time.Hour)},
		// Exited container from a dead owner — safe to remove.
		{Name: "swe-runC-exited", Running: false, Owner: "host:c", CreatedAt: now.Add(-2 * time.Hour)},
		// Orphaned running container past the stale TTL — remove.
		{Name: "swe-runD-old", Running: true, Owner: "host:d", CreatedAt: now.Add(-2 * time.Hour)},
		// Orphaned running container still within the stale grace — keep.
		{Name: "swe-runE-young", Running: true, Owner: "host:e", CreatedAt: now.Add(-1 * time.Minute)},
		// Legacy container with no owner label, exited — remove.
		{Name: "swe-legacy-exited", Running: false, Owner: "", CreatedAt: time.Time{}},
	}

	remove := selectContainersForCleanup(infos, now, staleTTL, alive)

	removed := map[string]bool{}
	for _, n := range remove {
		removed[n] = true
	}
	if removed["swe-runB-inst"] {
		t.Fatal("ownership-scoped cleanup must NOT remove a concurrent live harness's running container")
	}
	if removed["swe-runE-young"] {
		t.Error("must not remove an orphaned running container still within the stale grace")
	}
	for _, want := range []string{"swe-runC-exited", "swe-runD-old", "swe-legacy-exited"} {
		if !removed[want] {
			t.Errorf("expected %q to be removed", want)
		}
	}
}

func TestIsOwnerAlive(t *testing.T) {
	if isOwnerAlive("") {
		t.Error("empty owner (legacy container) must not be treated as live")
	}
	if isOwnerAlive("host-without-pid") {
		t.Error("unparseable owner must not be treated as live")
	}
	// A different host cannot be probed — treat as alive so we never remove it.
	if !isOwnerAlive("some-other-host:1") {
		t.Error("a container on a different host must be treated as alive")
	}
	// This process's own owner string must be reported alive.
	if !isOwnerAlive(harnessOwner()) {
		t.Error("this harness's own owner must be reported alive")
	}
}

func TestNewRunID_CollisionResistant(t *testing.T) {
	a := newRunID()
	b := newRunID()
	if a == b {
		t.Errorf("two run IDs generated back-to-back collided: %q", a)
	}
	// Must retain the second-resolution timestamp prefix plus a random suffix.
	if len(strings.Split(a, "-")) < 3 {
		t.Errorf("run ID %q missing timestamp+random structure", a)
	}
}

// --- #608: deadline vs watchdog ----------------------------------------------

func TestWatchdogTimeoutExceedsBenchmarkDeadline(t *testing.T) {
	r := &DockerRunner{Timeout: 30 * time.Minute, WatchdogGrace: 60 * time.Second}
	if got := r.watchdogTimeout(); got <= r.Timeout {
		t.Errorf("watchdogTimeout() = %v, must exceed benchmark deadline %v so the child can emit its summary", got, r.Timeout)
	}
	// Grace defaults to a positive value when unset.
	rDefault := &DockerRunner{Timeout: 10 * time.Minute}
	if got := rDefault.watchdogTimeout(); got <= rDefault.Timeout {
		t.Errorf("watchdogTimeout() with default grace = %v, must exceed %v", got, rDefault.Timeout)
	}
}

func TestClassifyAgentRun_CleanChildTimeoutPreservesSummary(t *testing.T) {
	// The child hit its own deadline, emitted a "timeout" summary, then exited
	// non-zero. The host watchdog did NOT fire (watchdogFired=false).
	childSummary := AgentSummary{Turns: 12, TerminationReason: "timeout"}
	childErr := errors.New("docker exec swe-x: exit status 1")

	summary, err := classifyAgentRun(childSummary, childErr, false)

	if summary.TerminationReason != "timeout" {
		t.Errorf("clean child timeout must keep its own reason, got %q", summary.TerminationReason)
	}
	if summary.Turns != 12 {
		t.Errorf("child summary data must be preserved, got Turns=%d", summary.Turns)
	}
	if errors.Is(err, errWatchdogKill) {
		t.Error("a clean child timeout must NOT be classified as a watchdog kill")
	}
	if err == nil {
		t.Error("the child exec error must still propagate")
	}
}

func TestClassifyAgentRun_WatchdogKillIsDistinct(t *testing.T) {
	// The child hung past deadline+grace; the host watchdog force-killed the exec.
	summary, err := classifyAgentRun(AgentSummary{}, context.DeadlineExceeded, true)

	if summary.TerminationReason != terminationWatchdogKill {
		t.Errorf("watchdog kill reason = %q, want %q", summary.TerminationReason, terminationWatchdogKill)
	}
	if !errors.Is(err, errWatchdogKill) {
		t.Errorf("watchdog kill must carry errWatchdogKill, got %v", err)
	}
}
