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

	"golang.org/x/sys/unix"

	"github.com/2389-research/tracker/agent"
	execpkg "github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/internal/testutil"
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
		{
			// #658: RODirs("/") alone made `> /dev/null` fail "Permission
			// denied" and `cmd 2>/dev/null` exit 2 without running cmd. The
			// in-jail write is chained behind both shapes so it only lands
			// when the /dev/null carve-out (RWFiles) is in place.
			name:           "redirect to /dev/null allowed inside jail",
			cmdTemplate:    "echo probe > /dev/null && true 2>/dev/null && echo allowed > %s/ok.txt",
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

			// Run the command through the jailed env. Capture and verify
			// the run actually started — without this, a missing `sh` or a
			// __jail-exec dispatch failure would make the negative-path
			// rows pass vacuously (#272 review, Copilot e2e:97).
			res, runErr := env.ExecCommand(context.Background(), "sh", []string{"-c", cmd}, 5*time.Second)
			if runErr != nil {
				t.Fatalf("ExecCommand startup failed (test cannot prove enforcement): %v", runErr)
			}
			_ = res // exit code intentionally not asserted: sh emits a
			// non-zero exit on redirect failure but tracker only stamps
			// ExitCode for *exec.ExitError; both signals point at the
			// filesystem assertions below, which are the authoritative check.

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
			name: "Landlock unavailable (skipped on Linux 6.2+)",
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
			if tc.name == "Landlock unavailable (skipped on Linux 6.2+)" {
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

// TestParallelBranchSymlinkRace proves the in-process write tier (openat2
// RESOLVE_NO_SYMLINKS, spec D6) rejects a write whose path component a
// sibling branch swaps to a symlink WHILE the write is in flight — and
// proves it by witnessing both sides, not by scheduler luck (#658).
//
// History. The original (#275, fe4493a) ran an unbounded jailed
// `rm -rf share && ln -sfn` forge loop against 200 in-process writes. It
// fork-bombed a 4-core host (213f42f: load avg 74, 75 live __jail-exec
// children) and was bounded to 100 forks, and #658 then showed it was
// vacuous in both directions: one jailed re-exec (~7–10 ms) costs about as
// much as B's whole write loop (~3–8 ms), so at most ONE forge ever
// overlapped. When it did, GNU rm hit "Directory not empty" against B's
// SafeMkdirAll (exit 1, err=nil, uncounted → the forge-side vacuity guard
// fired on ~22% of CI attempts). When it did not, all 200 writes succeeded
// BEFORE the symlink landed, so RESOLVE_NO_SYMLINKS was never exercised.
// And `ln -sfn TARGET existing-dir` exits 0 and plants the link INSIDE the
// dir B re-created, so a "counted" forge may never have put a symlink at
// the raced path at all.
//
// Design now:
//  1. Exactly ONE jailed fork. Branch A's Bash plants the first symlink from
//     inside its jail (the D11 "A forges from inside its jail" witness),
//     asserted with exit 0 AND Lstat ModeSymlink — else Fatalf with err /
//     exit code / stderr. One fork, not a loop: a jailed re-exec is too slow
//     to race B and too expensive to spawn in bulk (the #272/#275 fork bomb).
//  2. An in-process flipper toggles branchA/share between a real dir and a
//     symlink to outsideDir with single-syscall steps (rename / symlink /
//     mkdir), so neither side can starve the other. EEXIST — B's
//     SafeMkdirAll won the gap — is counted, not fatal.
//  3. Branch B writes through branchA/share/payload-N.txt until ALL
//     witnesses hold: ok>=1, rejected>=1 (ErrPathEscape / ELOOP / EXDEV), B
//     observed both a dir and a symlink at share, flips>=20 — or the 5 s
//     deadline fails the test with every counter printed.
//  4. outsideDir stays empty: the security assertion.
func TestParallelBranchSymlinkRace(t *testing.T) {
	if err := execpkg.ProbeLandlock(); err != nil {
		t.Skipf("Landlock unavailable: %v", err)
	}
	// Branches share `anchor` per pipeline/handlers/parallel.go. Branch B's
	// jail is deliberately broad enough to include the path A is racing —
	// the only shape that exercises the TOCTOU (#272 review, coderabbitai
	// e2e:310): if B's writable_paths did not overlap the path A mutates,
	// the kernel would never resolve through A's symlink at all.
	anchor := t.TempDir()
	workspaceA := filepath.Join(anchor, "branchA")
	share := filepath.Join(workspaceA, "share")
	if err := os.MkdirAll(share, 0755); err != nil {
		t.Fatal(err)
	}
	outsideDir := t.TempDir()

	envA := execpkg.NewLocalEnvironment(anchor)
	cfgA := agent.SessionConfig{WorkingDir: anchor, WritablePaths: []string{"branchA/**"}, WritablePathsSet: true, Backend: "native"}
	if _, err := configureJail(&cfgA, envA, anchor); err != nil {
		t.Fatalf("configureJail A: %v", err)
	}
	envB := execpkg.NewLocalEnvironment(anchor)
	cfgB := agent.SessionConfig{WorkingDir: anchor, WritablePaths: []string{"branchA/share/**"}, WritablePathsSet: true, Backend: "native"}
	if _, err := configureJail(&cfgB, envB, anchor); err != nil {
		t.Fatalf("configureJail B: %v", err)
	}

	t0 := time.Now()
	// Step 1 — the one and only fork. Branch A's jailed Bash plants the
	// symlink. It must succeed or the test cannot claim the vector was
	// exercised from inside a jail.
	res, err := envA.ExecCommand(context.Background(), "sh", []string{"-c",
		fmt.Sprintf("rm -rf %q && ln -sfn %q %q", share, outsideDir, share)}, 5*time.Second)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("jailed forge failed: err=%v exit=%d stderr=%q", err, res.ExitCode, res.Stderr)
	}
	if lst, err := os.Lstat(share); err != nil || lst.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("jailed forge exited 0 but %s is not a symlink (lstat err=%v); stderr=%q", share, err, res.Stderr)
	}
	forkDone := time.Since(t0)

	w := testutil.NewWitness("ok", "rejected", "transient", "other", "sawDir", "sawLink", "flips", "flipErrs")

	// Step 2 — the in-process flipper. Each step is one syscall, so B cannot
	// starve it and it cannot starve B.
	stop := make(chan struct{})
	flipperDone := make(chan struct{})
	go func() {
		defer close(flipperDone)
		for n := 0; ; n++ {
			select {
			case <-stop:
				return
			default:
			}
			gone := fmt.Sprintf("%s.gone.%d", share, n)
			_ = os.Rename(share, gone) // dir or symlink; ENOENT if B has not re-created it yet
			if err := os.Symlink(outsideDir, share); err == nil {
				w.Hit("flips")
			} else {
				w.Hit("flipErrs") // EEXIST: B's SafeMkdirAll won the gap — fine, next round
			}
			_ = os.RemoveAll(gone)
			_ = os.Remove(share) // removes the symlink (or an empty dir) only
			_ = os.Mkdir(share, 0755)
		}
	}()

	// Step 3 — branch B writes until every witness holds or the deadline.
	const wantFlips = 20
	allWitnessed := func() bool {
		return w.Count("ok") >= 1 && w.Count("rejected") >= 1 &&
			w.Count("sawDir") >= 1 && w.Count("sawLink") >= 1 &&
			w.Count("flips") >= wantFlips
	}
	var otherSample []string
	i := 0
	witnessed, writes := testutil.Overlap(t, 5*time.Second, func() bool {
		i++
		if lst, err := os.Lstat(share); err == nil {
			switch {
			case lst.Mode()&os.ModeSymlink != 0:
				w.Hit("sawLink")
			case lst.IsDir():
				w.Hit("sawDir")
			}
		}
		// i%8 keeps the dir small so the flipper's RemoveAll stays cheap.
		err := envB.WriteFile(context.Background(), fmt.Sprintf("branchA/share/payload-%d.txt", i%8), "ok")
		switch {
		case err == nil:
			w.Hit("ok")
		case errors.Is(err, execpkg.ErrPathEscape), errors.Is(err, unix.ELOOP), errors.Is(err, unix.EXDEV):
			w.Hit("rejected")
		case errors.Is(err, unix.ENOENT), errors.Is(err, unix.ENOTDIR):
			w.Hit("transient") // share vanished between the flipper's rename and re-create
		default:
			w.Hit("other")
			if len(otherSample) < 3 {
				otherSample = append(otherSample, err.Error())
			}
		}
		return allWitnessed()
	})
	close(stop)
	<-flipperDone

	t.Logf("witness: %s writes=%d fork=%s total=%s other=%v", w, writes, forkDone, time.Since(t0), otherSample)

	// Step 4 — the security assertion.
	if entries, _ := os.ReadDir(outsideDir); len(entries) > 0 {
		t.Errorf("outsideDir has %d entries; a write resolved through A's symlink: %v (%s)", len(entries), entries, w)
	}
	if !witnessed {
		t.Fatalf("overlap not witnessed within 5s after %d writes (want ok>=1 rejected>=1 sawDir>=1 sawLink>=1 flips>=%d): %s; forge took %s; other errors: %v",
			writes, wantFlips, w, forkDone, otherSample)
	}
	w.Require(t, 1, "ok", "rejected", "sawDir", "sawLink")
	w.Require(t, wantFlips, "flips")
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
