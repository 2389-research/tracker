// ABOUTME: Real CLI run regression for routing on escaped quotes and logical delimiters in tool output.
// ABOUTME: Uses production tool handlers and a temporary workdir without network, credentials, or mocks.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRoutesQuotedLogicalDelimiterToolOutput(t *testing.T) {
	workdir := t.TempDir()
	workflow := filepath.Join(workdir, "quoted_route.dip")
	source := `workflow quoted_route
  start: Emit
  exit: Done

  tool Emit
    command:
      printf 'say "alpha||beta"'

  tool Matched
    command:
      printf 'routed\n' > routed.txt

  tool Wrong
    command:
      printf 'wrong route executed\n' >&2
      exit 23

  tool Done
    command:
      test "$(cat routed.txt)" = routed

  edges
    Emit -> Matched when ctx.tool_stdout = "say \"alpha||beta\""
    Emit -> Wrong
    Matched -> Done
    Wrong -> Done when ctx.outcome = success
`
	if err := os.WriteFile(workflow, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	originalPolicy := activeGitConfig.policy
	originalAllowInit := activeGitConfig.allowInit
	activeGitConfig.policy = "off"
	activeGitConfig.allowInit = false
	t.Cleanup(func() {
		activeGitConfig.policy = originalPolicy
		activeGitConfig.allowInit = originalAllowInit
	})
	t.Setenv("TRACKER_NO_UPDATE_CHECK", "1")

	output, err := captureRunOutput(t, func() error {
		return run(workflow, workdir, "", "dip", "acp", false, false)
	})
	if err != nil {
		t.Fatalf("run returned error: %v\n%s", err, output)
	}
	routed, err := os.ReadFile(filepath.Join(workdir, "routed.txt"))
	if err != nil {
		t.Fatalf("read routed.txt: %v\n%s", err, output)
	}
	if string(routed) != "routed\n" {
		t.Fatalf("routed.txt = %q, want %q", routed, "routed\n")
	}
	lowerOutput := strings.ToLower(output)
	for _, unwanted := range []string{"warning:", "error:", "wrong route executed", "update available"} {
		if strings.Contains(lowerOutput, unwanted) {
			t.Fatalf("run output contains %q:\n%s", unwanted, output)
		}
	}
}

func captureRunOutput(t *testing.T, runFunc func() error) (string, error) {
	t.Helper()
	stdout, err := os.CreateTemp(t.TempDir(), "stdout-*.log")
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.CreateTemp(t.TempDir(), "stderr-*.log")
	if err != nil {
		t.Fatal(err)
	}
	originalStdout, originalStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdout, stderr
	defer func() {
		os.Stdout, os.Stderr = originalStdout, originalStderr
		_ = stdout.Close()
		_ = stderr.Close()
	}()
	runErr := runFunc()
	os.Stdout, os.Stderr = originalStdout, originalStderr
	if err := stdout.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderr.Close(); err != nil {
		t.Fatal(err)
	}
	stdoutBytes, err := os.ReadFile(stdout.Name())
	if err != nil {
		t.Fatal(err)
	}
	stderrBytes, err := os.ReadFile(stderr.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(stdoutBytes) + string(stderrBytes), runErr
}

func TestCaptureRunOutputRestoresStreamsAfterPanic(t *testing.T) {
	originalStdout, originalStderr := os.Stdout, os.Stderr
	func() {
		defer func() { _ = recover() }()
		_, _ = captureRunOutput(t, func() error { panic("boom") })
	}()
	gotStdout, gotStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = originalStdout, originalStderr
	if gotStdout != originalStdout || gotStderr != originalStderr {
		_ = gotStdout.Close()
		_ = gotStderr.Close()
		t.Fatal("captureRunOutput did not restore stdout and stderr after panic")
	}
}
