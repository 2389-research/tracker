//go:build linux

package handlers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/tracker/agent"
	execpkg "github.com/2389-research/tracker/agent/exec"
)

// TestMain dispatches to the __jail-exec helper when this test binary is
// re-invoked by WrapBashCmd via /proc/self/exe. Without this, the re-exec
// child starts running tests instead of applying Landlock.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "__jail-exec" {
		os.Exit(execpkg.RunJailExec(os.Args[2:]))
	}
	os.Exit(m.Run())
}

func TestWritablePathsEnforcement(t *testing.T) {
	if err := execpkg.ProbeLandlock(); err != nil {
		t.Skipf("Landlock unavailable: %v", err)
	}

	type row struct {
		name           string
		cmdTemplate    string // %s placeholders filled with: inside dir, outside dir
		assertInsideOK bool   // a file at <inside>/ok.txt must exist
		assertOutside  string // expected outcome at <outside>/escape.txt: "denied" or "" (no assertion)
	}

	cases := []row{
		{
			name:          "direct out-of-jail write denied",
			cmdTemplate:   "echo pwned > %s/escape.txt",
			assertOutside: "denied",
		},
		{
			name:          "child process out-of-jail write denied",
			cmdTemplate:   "sh -c 'echo pwned > %s/escape.txt'",
			assertOutside: "denied",
		},
		{
			name:           "in-jail write succeeds",
			cmdTemplate:    "echo allowed > %s/ok.txt",
			assertInsideOK: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			anchor := t.TempDir()
			workspace := filepath.Join(anchor, "workspace")
			outsideRoot := filepath.Join(t.TempDir(), "outside")
			if err := os.MkdirAll(workspace, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(outsideRoot, 0755); err != nil {
				t.Fatal(err)
			}

			env := execpkg.NewLocalEnvironment(anchor)
			cfg := agent.SessionConfig{
				WorkingDir:       anchor,
				WritablePaths:    []string{"workspace/**"},
				WritablePathsSet: true,
				Backend:          "native",
			}
			if _, err := configureJail(&cfg, env, anchor); err != nil {
				t.Fatalf("configureJail: %v", err)
			}

			// Substitute the target dir into the command template.
			var cmd string
			switch {
			case tc.assertInsideOK:
				cmd = fmt.Sprintf(tc.cmdTemplate, workspace)
			case tc.assertOutside != "":
				cmd = fmt.Sprintf(tc.cmdTemplate, outsideRoot)
			default:
				t.Fatalf("test row has neither inside nor outside assertion: %s", tc.name)
			}

			// Run the command through the jailed env. Output ignored; the
			// assertion is on the resulting filesystem state, since shells
			// generally print errors but exit non-zero only on the last command.
			_, _ = env.ExecCommand(context.Background(), "sh", []string{"-c", cmd}, 5*time.Second)

			if tc.assertInsideOK {
				okPath := filepath.Join(workspace, "ok.txt")
				if _, err := os.Stat(okPath); err != nil {
					t.Errorf("inside write was blocked: %v", err)
				}
			}
			if tc.assertOutside == "denied" {
				escapePath := filepath.Join(outsideRoot, "escape.txt")
				if _, err := os.Stat(escapePath); err == nil {
					t.Errorf("outside write succeeded; jail did not enforce. File: %s", escapePath)
				}
			}
		})
	}
}

func TestWorkingDirRelocationRefused(t *testing.T) {
	if err := execpkg.ProbeLandlock(); err != nil {
		t.Skipf("Landlock unavailable: %v", err)
	}
	processCwd := t.TempDir()
	env := execpkg.NewLocalEnvironment(processCwd)
	cfg := agent.SessionConfig{
		WorkingDir:       "/tmp/atk",
		WritablePaths:    []string{"workspace/**"},
		WritablePathsSet: true,
		Backend:          "native",
	}
	_, err := configureJail(&cfg, env, processCwd)
	if err == nil {
		t.Fatal("configureJail with working_dir: /tmp/atk = nil error; want refuse")
	}
	if !errors.Is(err, execpkg.ErrPathEscape) {
		t.Errorf("err = %v, want errors.Is(err, ErrPathEscape)", err)
	}
}

func TestWritablePathsFailClosed(t *testing.T) {
	cases := []struct {
		name    string
		cfg     agent.SessionConfig
		wantSub string
	}{
		{
			name: "empty list",
			cfg: agent.SessionConfig{
				WorkingDir:       ".",
				WritablePaths:    []string{},
				WritablePathsSet: true,
				Backend:          "native",
			},
			wantSub: "empty",
		},
		{
			name: "malformed brace",
			cfg: agent.SessionConfig{
				WorkingDir:       ".",
				WritablePaths:    []string{"workspace/*.{md"},
				WritablePathsSet: true,
				Backend:          "native",
			},
			wantSub: "malformed",
		},
		{
			name: "Landlock unavailable (skipped on Linux 6.7+)",
			cfg: agent.SessionConfig{
				WorkingDir:       ".",
				WritablePaths:    []string{"workspace/**"},
				WritablePathsSet: true,
				Backend:          "native",
			},
			wantSub: "landlock",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "Landlock unavailable (skipped on Linux 6.7+)" {
				if err := execpkg.ProbeLandlock(); err == nil {
					t.Skip("Landlock available; cannot exercise this refusal path")
				}
			}
			env := execpkg.NewLocalEnvironment(t.TempDir())
			processCwd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			_, gotErr := configureJail(&tc.cfg, env, processCwd)
			if gotErr == nil {
				t.Fatal("configureJail = nil; want refuse")
			}
			if !strings.Contains(strings.ToLower(gotErr.Error()), tc.wantSub) {
				t.Errorf("err = %v, want substring %q", gotErr, tc.wantSub)
			}
		})
	}
}

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
		WorkingDir:       anchor,
		WritablePaths:    []string{"workspace/**"},
		WritablePathsSet: true,
		Backend:          "native",
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

	// Branches share `anchor` per pipeline/handlers/parallel.go:162.
	// Branch A's Bash runs a tight symlink-forge loop inside its workspace.
	// Branch B's in-process Write tries to land files in branchB. Without
	// openat2's RESOLVE_BENEATH + RESOLVE_NO_SYMLINKS, branch B would race
	// branch A's symlink swap; with the kernel atomic-check, the race is
	// closed.

	outsideDir := t.TempDir()

	envA := execpkg.NewLocalEnvironment(anchor)
	cfgA := agent.SessionConfig{
		WorkingDir:       anchor,
		WritablePaths:    []string{"branchA/**"},
		WritablePathsSet: true,
		Backend:          "native",
	}
	if _, err := configureJail(&cfgA, envA, anchor); err != nil {
		t.Fatalf("configureJail A: %v", err)
	}

	envB := execpkg.NewLocalEnvironment(anchor)
	cfgB := agent.SessionConfig{
		WorkingDir:       anchor,
		WritablePaths:    []string{"branchB/**"},
		WritablePathsSet: true,
		Backend:          "native",
	}
	if _, err := configureJail(&cfgB, envB, anchor); err != nil {
		t.Fatalf("configureJail B: %v", err)
	}

	// Goroutine A: forges symlinks in a bounded loop. The cap (maxForges)
	// prevents fork-bombing the host if branch B stalls or the goroutine
	// fails to observe `stop` promptly. Each iteration spawns one
	// /proc/self/exe __jail-exec child via WrapBashCmd; an unbounded loop
	// has overwhelmed 4-core hosts in practice. 100 iterations is more
	// than enough to expose any kernel-level race window for the writes
	// branch B is doing concurrently.
	const maxForges = 100
	stop := make(chan struct{})
	forgeDone := make(chan struct{})
	go func() {
		defer close(forgeDone)
		for i := 0; i < maxForges; i++ {
			select {
			case <-stop:
				return
			default:
			}
			// Forge a symlink inside branchA pointing to outsideDir.
			// ln -sfn replaces atomically if the link already exists.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, _ = envA.ExecCommand(ctx, "sh",
				[]string{"-c", fmt.Sprintf("ln -sfn %s %s/share", outsideDir, workspaceA)}, 2*time.Second)
			cancel()
		}
	}()

	// Branch B: races writes through any "share" path it can construct.
	// Even if A's symlink ends up at branchA/share pointing outside, B's
	// writes target branchB/payload-N.txt — they shouldn't reach outsideDir.
	// But if envB.WriteFile EVER follows a symlink and ends up writing
	// outside, we have a race vector.
	for i := 0; i < 200; i++ {
		relPath := fmt.Sprintf("branchB/payload-%d.txt", i)
		_ = envB.WriteFile(context.Background(), relPath, "ok")
	}

	close(stop)
	<-forgeDone

	entries, _ := os.ReadDir(outsideDir)
	if len(entries) > 0 {
		t.Errorf("outsideDir has %d entries; race let a write through. Entries: %v", len(entries), entries)
	}
}

func TestInProcessWriteEnforcesExactGlob(t *testing.T) {
	if err := execpkg.ProbeLandlock(); err != nil {
		t.Skipf("Landlock unavailable: %v", err)
	}
	// File-scoped glob — only workspace/allowed.md may be written.
	anchor := t.TempDir()
	workspace := filepath.Join(anchor, "workspace")
	if err := os.MkdirAll(workspace, 0755); err != nil {
		t.Fatal(err)
	}
	env := execpkg.NewLocalEnvironment(anchor)
	cfg := agent.SessionConfig{
		WorkingDir:       anchor,
		WritablePaths:    []string{"workspace/allowed.md"},
		WritablePathsSet: true,
		Backend:          "native",
	}
	if _, err := configureJail(&cfg, env, anchor); err != nil {
		t.Fatalf("configureJail: %v", err)
	}

	// Allowed file: must succeed.
	if err := env.WriteFile(context.Background(), "workspace/allowed.md", "ok"); err != nil {
		t.Errorf("write to allowed file failed: %v", err)
	}

	// In-anchor but not in glob: must FAIL with ErrPathNotAllowed.
	err := env.WriteFile(context.Background(), "workspace/forbidden.md", "no")
	if err == nil {
		t.Fatal("write to non-matching file succeeded; spec D2 in-process exact-glob enforcement is missing")
	}
	if !errors.Is(err, execpkg.ErrPathNotAllowed) {
		t.Errorf("err = %v, want errors.Is(err, ErrPathNotAllowed)", err)
	}

	// Outside the working directory (relative traversal): safePath blocks this
	// before WriteOpener is reached; any non-nil error satisfies the contract.
	err = env.WriteFile(context.Background(), "../outside.txt", "no")
	if err == nil {
		t.Fatal("write outside working directory succeeded")
	}
}
