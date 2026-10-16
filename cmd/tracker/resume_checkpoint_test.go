// ABOUTME: Tests that the CLI resume path (-r) resolves checkpoints through the
// ABOUTME: library resolver: honors --artifact-dir and reads the secure copy first (#696, #559).
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// resolveCLICheckpoint drives the real CLI resume resolution
// (resolveRunCheckpoint) and returns the checkpoint path it picked.
func resolveCLICheckpoint(t *testing.T, workdir, artifactDir, runID string) (string, error) {
	t.Helper()
	info, err := resolveRunCheckpoint(runConfig{
		workdir:      workdir,
		artifactDir:  artifactDir,
		resumeID:     runID,
		pipelineFile: "pipeline.dip",
	})
	return info.CheckpointPath, err
}

func plantCLICheckpoint(t *testing.T, path, runID, gate string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := pipeline.SaveCheckpoint(&pipeline.Checkpoint{
		RunID: runID, EdgeSelections: map[string]string{"Gate": gate},
	}, path); err != nil {
		t.Fatal(err)
	}
}

// TestResolveRunCheckpoint_HonorsArtifactDir: a run started with
// --artifact-dir D lives in D/<runID>/, so `tracker -r <id> --artifact-dir D`
// must resolve there instead of failing on <workdir>/.tracker/runs (#696).
func TestResolveRunCheckpoint_HonorsArtifactDir(t *testing.T) {
	t.Setenv("TRACKER_AUDIT_DIR", t.TempDir())
	wd, ad := t.TempDir(), t.TempDir()
	const runID = "0123456789ab"
	legacy := filepath.Join(ad, runID, "checkpoint.json")
	plantCLICheckpoint(t, legacy, runID, "Approve")

	got, err := resolveCLICheckpoint(t, wd, ad, "0123")
	if err != nil {
		t.Fatalf("resume with --artifact-dir failed: %v", err)
	}
	if got != legacy {
		t.Fatalf("got %q, want %q", got, legacy)
	}
}

// TestResolveRunCheckpoint_PrefersSecureOverTamperedSnapshot is the #559
// invariant for the CLI path: `tracker -r` must read the authoritative secure
// checkpoint, never the tool-reachable workdir snapshot.
func TestResolveRunCheckpoint_PrefersSecureOverTamperedSnapshot(t *testing.T) {
	t.Setenv("TRACKER_AUDIT_DIR", t.TempDir())
	wd := t.TempDir()
	const runID = "0123456789cd"
	plantCLICheckpoint(t, filepath.Join(wd, ".tracker", "runs", runID, "checkpoint.json"), runID, "SkipReview")
	secure, err := pipeline.SecureCheckpointPath(runID)
	if err != nil {
		t.Fatal(err)
	}
	plantCLICheckpoint(t, secure, runID, "Approve")

	got, err := resolveCLICheckpoint(t, wd, "", runID)
	if err != nil {
		t.Fatalf("resolveRunCheckpoint: %v", err)
	}
	if got != secure {
		t.Fatalf("CLI resume resolved to %q, want secure %q", got, secure)
	}
	cp, err := pipeline.LoadCheckpoint(got)
	if err != nil {
		t.Fatal(err)
	}
	if cp.EdgeSelections["Gate"] != "Approve" {
		t.Fatalf("CLI resume loaded TAMPERED routing %q, want %q", cp.EdgeSelections["Gate"], "Approve")
	}
}

// TestResolveRunCheckpoint_FullIDOutsideArtifactDirFails (#696 review): a run
// started with --artifact-dir D, resumed by its full ID without it, must fail.
// The secure checkpoint exists, but resuming from it would write the run's
// artifacts to <workdir>/.tracker/runs and split the run across two dirs.
func TestResolveRunCheckpoint_FullIDOutsideArtifactDirFails(t *testing.T) {
	t.Setenv("TRACKER_AUDIT_DIR", t.TempDir())
	wd, ad := t.TempDir(), t.TempDir()
	const runID = "0123456789ab"
	plantCLICheckpoint(t, filepath.Join(ad, runID, "checkpoint.json"), runID, "Approve")
	secure, err := pipeline.SecureCheckpointPath(runID)
	if err != nil {
		t.Fatal(err)
	}
	plantCLICheckpoint(t, secure, runID, "Approve")

	got, err := resolveCLICheckpoint(t, wd, "", runID)
	if err == nil {
		t.Fatalf("resume without the run's --artifact-dir resolved to %q, want an error", got)
	}
	if !strings.Contains(err.Error(), "--artifact-dir") {
		t.Fatalf("error should hint at --artifact-dir, got: %v", err)
	}
}

// TestResumeHintTarget: the printed resume command must carry the run's
// --artifact-dir and an explicit -w, absolute and shell-quoted, or copying it
// resumes in the wrong dir (#696).
func TestResumeHintTarget(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		opts runOptions
		want string
	}{
		{"default dirs", runOptions{pipelineFile: "p.dip", workdir: "/w"}, "p.dip"},
		{"artifact dir", runOptions{pipelineFile: "p.dip", workdir: "/w", artifactDir: "/tmp/ad"}, "--artifact-dir /tmp/ad p.dip"},
		{"explicit workdir", runOptions{pipelineFile: "p.dip", workdir: "/w", workdirExplicit: true}, "-w /w p.dip"},
		{"both", runOptions{pipelineFile: "p.dip", workdir: "/w", workdirExplicit: true, artifactDir: "/ad"}, "-w /w --artifact-dir /ad p.dip"},
		{"relative dirs made absolute", runOptions{pipelineFile: "p.dip", workdir: "proj", workdirExplicit: true, artifactDir: "ad"},
			"-w " + filepath.Join(cwd, "proj") + " --artifact-dir " + filepath.Join(cwd, "ad") + " p.dip"},
		{"spaces quoted", runOptions{pipelineFile: "my p.dip", workdir: "/my w", workdirExplicit: true, artifactDir: "/it's"},
			`-w '/my w' --artifact-dir '/it'\''s' 'my p.dip'`},
		{"no pipeline file", runOptions{artifactDir: "/ad"}, "--artifact-dir /ad <pipeline.dip>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.opts.resumeHintTarget(); got != tc.want {
				t.Fatalf("resumeHintTarget = %q, want %q", got, tc.want)
			}
		})
	}
}

// failedResult is a failed run the summary prints a resume hint for.
func failedResult() *pipeline.EngineResult {
	return &pipeline.EngineResult{RunID: "abcd1234", Status: pipeline.OutcomeFail}
}

// TestFinishRun_ResumeHintCarriesDirs: the resume command printed after a
// failed console run carries --artifact-dir and -w (#696 review: the wiring,
// not only the helper).
func TestFinishRun_ResumeHintCarriesDirs(t *testing.T) {
	opts := &runOptions{pipelineFile: "p.dip", workdir: "/w", workdirExplicit: true, artifactDir: "/ad"}
	out := captureStdout(t, func() {
		_ = finishRun(failedResult(), nil, opts, nil, "/ad")
	})
	if want := "tracker -r abcd1234 -w /w --artifact-dir /ad p.dip"; !strings.Contains(out, want) {
		t.Fatalf("summary missing %q:\n%s", want, out)
	}
}

// TestFinishTUIRun_ResumeHintCarriesDirs is the same check for the TUI path.
func TestFinishTUIRun_ResumeHintCarriesDirs(t *testing.T) {
	t.Setenv("TRACKER_NO_NOTIFY", "1")
	opts := &runOptions{pipelineFile: "p.dip", workdir: "/w", workdirExplicit: true, artifactDir: "/ad"}
	out := captureStdout(t, func() {
		_ = finishTUIRun(pipelineOutcome{result: failedResult()}, "p", opts, nil, "/ad")
	})
	if want := "tracker -r abcd1234 -w /w --artifact-dir /ad p.dip"; !strings.Contains(out, want) {
		t.Fatalf("summary missing %q:\n%s", want, out)
	}
}
