// ABOUTME: Unit tests for Docker container lifecycle helpers in the swebench harness.
// ABOUTME: Tests cover helper functions only — no actual Docker daemon is required.
package main

import (
	"strings"
	"testing"
)

func TestContainerName(t *testing.T) {
	got := containerName("django__django-11095")
	want := "swe-django__django-11095"
	if got != want {
		t.Errorf("containerName() = %q, want %q", got, want)
	}
}

func TestBuildCloneCmd(t *testing.T) {
	got := buildCloneCmd(
		"https://github.com/django/django.git",
		"abc123",
		"/workspace",
		"/cache/django",
	)
	// Must be a sh -c invocation
	if len(got) < 3 {
		t.Fatalf("expected at least 3 elements, got %d: %v", len(got), got)
	}
	if got[0] != "sh" || got[1] != "-c" {
		t.Errorf("expected [sh -c ...], got %v", got[:2])
	}
	cmd := got[2]
	if !strings.Contains(cmd, "--reference /cache/django") {
		t.Errorf("expected --reference flag in cmd, got: %s", cmd)
	}
	if !strings.Contains(cmd, "git clone") {
		t.Errorf("expected git clone in cmd, got: %s", cmd)
	}
	if !strings.Contains(cmd, "https://github.com/django/django.git") {
		t.Errorf("expected repo URL in cmd, got: %s", cmd)
	}
	if !strings.Contains(cmd, "abc123") {
		t.Errorf("expected commit in cmd, got: %s", cmd)
	}
	if !strings.Contains(cmd, "/workspace") {
		t.Errorf("expected workDir in cmd, got: %s", cmd)
	}
}

func TestBuildCloneCmd_NoCache(t *testing.T) {
	got := buildCloneCmd(
		"https://github.com/django/django.git",
		"abc123",
		"/workspace",
		"",
	)
	if len(got) < 3 {
		t.Fatalf("expected at least 3 elements, got %d: %v", len(got), got)
	}
	if got[0] != "sh" || got[1] != "-c" {
		t.Errorf("expected [sh -c ...], got %v", got[:2])
	}
	cmd := got[2]
	if strings.Contains(cmd, "--reference") {
		t.Errorf("expected no --reference flag when cachePath empty, got: %s", cmd)
	}
	if !strings.Contains(cmd, "git clone") {
		t.Errorf("expected git clone in cmd, got: %s", cmd)
	}
}

func TestBuildEnvFlags(t *testing.T) {
	env := map[string]string{
		"FOO": "bar",
		"BAZ": "qux",
	}
	flags := buildEnvFlags(env)

	// Must have even count (pairs of -e KEY=VAL)
	if len(flags)%2 != 0 {
		t.Fatalf("expected even number of flags, got %d: %v", len(flags), flags)
	}
	// Every even-indexed element must be "-e"
	for i := 0; i < len(flags); i += 2 {
		if flags[i] != "-e" {
			t.Errorf("flags[%d] = %q, want \"-e\"", i, flags[i])
		}
	}
	// Build a set of KEY=VAL pairs
	got := map[string]bool{}
	for i := 1; i < len(flags); i += 2 {
		got[flags[i]] = true
	}
	if !got["FOO=bar"] {
		t.Error("expected FOO=bar in flags")
	}
	if !got["BAZ=qux"] {
		t.Error("expected BAZ=qux in flags")
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
	output := "some log line\nanother line\n{\"turns\":5,\"input_tokens\":1000,\"output_tokens\":200,\"duration_ms\":3500}\n"
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
}

func TestParseAgentSummary_NoJSON(t *testing.T) {
	output := "some log line\nanother log line\nplain text ending"
	got := parseAgentSummary(output)
	if got.Turns != 0 || got.InputTokens != 0 || got.OutputTokens != 0 || got.DurationMs != 0 {
		t.Errorf("expected zero-value AgentSummary for non-JSON output, got %+v", got)
	}
}
