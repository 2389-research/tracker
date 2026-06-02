//go:build linux

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	trkexec "github.com/2389-research/tracker/agent/exec"
)

func TestJailExecDispatch(t *testing.T) {
	// Skip on ANY non-nil ProbeLandlock error, not just ErrLandlockUnavailable —
	// restricted CI environments can fail with EPERM/seccomp without the
	// sentinel wrap, and the dispatch enforcement can't be exercised when
	// Landlock isn't reachable for any reason (#275 review, Copilot
	// jail_exec_test.go:18). Matches the broader-skip pattern used by every
	// other Landlock-gated test in this PR.
	if err := trkexec.ProbeLandlock(); err != nil {
		t.Skipf("Landlock unavailable: %v", err)
	}

	// Build tracker into a temp file so we don't depend on a pre-built
	// tracker on PATH.
	bin := filepath.Join(t.TempDir(), "tracker")
	buildCmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v. Output: %s", err, out)
	}

	// Invoke tracker __jail-exec with a deny-then-allow scenario.
	anchor := t.TempDir()
	if err := os.MkdirAll(filepath.Join(anchor, "workspace"), 0755); err != nil {
		t.Fatal(err)
	}
	insidePath := filepath.Join(anchor, "workspace", "ok.txt")
	outsideRoot := t.TempDir()
	outsidePath := filepath.Join(outsideRoot, "escape.txt")

	cmdStr := "echo allowed > " + insidePath + "; echo denied > " + outsidePath + " || true"
	runCmd := exec.Command(bin, "__jail-exec", "--", anchor, "workspace/**", "--",
		"sh", "-c", cmdStr)
	out, err := runCmd.CombinedOutput()
	t.Logf("tracker __jail-exec output: %s; err: %v", out, err)

	if _, statErr := os.Stat(insidePath); statErr != nil {
		t.Errorf("inside write was blocked: %v", statErr)
	}
	if _, statErr := os.Stat(outsidePath); statErr == nil {
		t.Errorf("outside write succeeded; jail did not enforce")
	}
}

func TestJailExecDispatch_NormalCLIStillWorks(t *testing.T) {
	// Regression: the dispatch should ONLY fire when os.Args[1] is
	// "__jail-exec" — normal CLI invocations must NOT be intercepted.
	bin := filepath.Join(t.TempDir(), "tracker")
	buildCmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v. Output: %s", err, out)
	}
	helpCmd := exec.Command(bin, "--help")
	out, _ := helpCmd.CombinedOutput()
	if len(out) == 0 {
		t.Errorf("tracker --help produced no output; dispatch may have hijacked normal CLI")
	}
}
