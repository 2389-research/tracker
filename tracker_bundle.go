// ABOUTME: Git bundle export for pipeline run artifact repositories.
// ABOUTME: ExportBundle wraps `git bundle create --all` so a completed run can be
// ABOUTME: shipped as a single portable file and restored with `git clone <bundle>`.
package tracker

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/2389-research/tracker/pipeline"
)

// bundleGitEnv returns the sanitized environment for ExportBundle's git
// subprocess. It delegates to pipeline.GitSafeEnv so bundle creation shares one
// env-filtering posture with the artifact-repo git calls instead of drifting:
// git-internal repository pointers (GIT_DIR/GIT_INDEX_FILE/...) are stripped
// unconditionally so `git -C runDir` is honored against runDir (not an inherited
// GIT_DIR that would bundle the OUTER repo), and credential-shaped vars are
// dropped unless TRACKER_PASS_ENV=1.
func bundleGitEnv() []string {
	return pipeline.GitSafeEnv()
}

// ExportBundle writes a git bundle of the run directory's artifact repository
// to outPath. The bundle captures all commits and tags (including checkpoint
// tags) produced by WithGitArtifacts during the run. Users can restore the
// run with `git clone <bundlePath> <dir>` and inspect history with `git log`.
//
// Returns an error if runDir is not a git repository, if git is not in PATH,
// or if `git bundle create` fails. The error wraps the command's stderr so
// callers can surface meaningful diagnostics.
//
// The bundle file is portable — it can be copied to another machine and
// cloned there without network access. This is the recommended way for a
// remote factory-worker instance to hand a completed run back to the user.
func ExportBundle(runDir, outPath string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git not found in PATH: %w", err)
	}
	absPath, err := filepath.Abs(outPath)
	if err != nil {
		return fmt.Errorf("resolve output path %q: %w", outPath, err)
	}
	cmd := exec.Command("git", "-C", runDir, "bundle", "create", absPath, "--all") //nolint:gosec
	cmd.Env = bundleGitEnv()
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git bundle create: %w\n%s", err, out.String())
	}
	return nil
}
