// ABOUTME: Git bundle export for pipeline run artifact repositories.
// ABOUTME: ExportBundle wraps `git bundle create --all` so a completed run can be
// ABOUTME: shipped as a single portable file and restored with `git clone <bundle>`.
package tracker

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// gitInternalEnvPointers are the git-internal repository pointers that, when
// present in the environment, override a command's `-C <dir>` and redirect it
// at the OUTER repository. ExportBundle addresses the artifact repo via `-C
// runDir`, so inheriting these (e.g. when tracker runs from inside a git hook)
// would bundle the wrong repo — or silently succeed against the ambient repo
// instead of failing for a non-repo runDir. Stripped before invoking git.
var gitInternalEnvPointers = map[string]bool{
	"GIT_DIR":              true,
	"GIT_INDEX_FILE":       true,
	"GIT_WORK_TREE":        true,
	"GIT_OBJECT_DIRECTORY": true,
	"GIT_COMMON_DIR":       true,
}

// bundleGitEnv returns os.Environ() with the git-internal repository pointers
// stripped so `git -C runDir` is honored against runDir, not an inherited
// GIT_DIR/GIT_INDEX_FILE.
func bundleGitEnv() []string {
	src := os.Environ()
	out := make([]string, 0, len(src))
	for _, e := range src {
		// Normalize key case before lookup (the map is upper-cased), matching
		// gitSafeEnv — so a mixed-case GIT_DIR can't slip past the strip.
		name := strings.ToUpper(strings.SplitN(e, "=", 2)[0])
		if !gitInternalEnvPointers[name] {
			out = append(out, e)
		}
	}
	return out
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
