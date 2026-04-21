// ABOUTME: Unit tests for Docker container lifecycle helpers in the swebench harness.
// ABOUTME: Tests cover helper functions only — no actual Docker daemon is required.
package main

import (
	"os"
	"strings"
	"testing"
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
