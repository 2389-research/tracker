// ABOUTME: Git-backed artifact tracking for pipeline runs.
// ABOUTME: Initializes the artifact dir as a git repo and commits after each terminal-outcome node.
package pipeline

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// gitArtifactRepo manages a git repo backing the artifact dir for a run.
// All operations are best-effort — if git fails, the engine logs a warning
// via the event handler and continues without git tracking (not a fatal error).
type gitArtifactRepo struct {
	dir    string
	runID  string
	failed bool // set true after first failure; subsequent ops no-op
}

// newGitArtifactRepo creates a new gitArtifactRepo for the given artifact run dir.
// The repo is not initialized until Init is called.
func newGitArtifactRepo(dir, runID string) *gitArtifactRepo {
	return &gitArtifactRepo{
		dir:   dir,
		runID: runID,
	}
}

// Init initializes the artifact directory as a (non-bare) git repo:
//  1. Ensures the directory exists.
//  2. Runs `git init --quiet` if .git is absent.
//  3. Sets local-only git user to tracker <tracker@local>.
//  4. Creates .gitignore if absent.
//  5. Makes an initial empty commit — but ONLY if the repo has no existing
//     history (i.e. this is a fresh run, not a resume against an existing
//     artifact directory).
//
// Returns an error if git is missing from PATH or initialization fails.
// Sets failed=true on error so subsequent calls are no-ops.
func (r *gitArtifactRepo) Init() error {
	if r.failed {
		return nil
	}

	// Verify git is available.
	if _, err := exec.LookPath("git"); err != nil {
		r.failed = true
		return fmt.Errorf("git not found in PATH: %w", err)
	}

	// Ensure the directory exists.
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		r.failed = true
		return fmt.Errorf("create artifact dir %q: %w", r.dir, err)
	}

	// Initialize git repo if .git doesn't already exist. Any Stat error
	// other than "not exist" (permission, IO) is treated as fatal so we
	// don't silently skip init and hit confusing downstream failures.
	gitDir := filepath.Join(r.dir, ".git")
	gitDirExists := false
	if _, err := os.Stat(gitDir); err == nil {
		gitDirExists = true
	} else if !errors.Is(err, os.ErrNotExist) {
		r.failed = true
		return fmt.Errorf("stat %q: %w", gitDir, err)
	}
	if !gitDirExists {
		if out, err := r.git("init", "--quiet"); err != nil {
			r.failed = true
			return fmt.Errorf("git init: %w\n%s", err, out)
		}
	}

	// Set local-only git user config so we don't pollute the global config.
	if out, err := r.git("config", "user.name", "tracker"); err != nil {
		r.failed = true
		return fmt.Errorf("git config user.name: %w\n%s", err, out)
	}
	if out, err := r.git("config", "user.email", "tracker@local"); err != nil {
		r.failed = true
		return fmt.Errorf("git config user.email: %w\n%s", err, out)
	}

	// Create .gitignore if absent. Missing .gitignore is non-fatal —
	// temp files will just show up in commits.
	gitignorePath := filepath.Join(r.dir, ".gitignore")
	if _, err := os.Stat(gitignorePath); errors.Is(err, os.ErrNotExist) {
		_ = os.WriteFile(gitignorePath, []byte("*.tmp\ncheckpoint.json\n"), 0o644)
	}

	// Only create the "run started" commit if the repo has no existing
	// HEAD. On checkpoint resume, the artifact dir already has history
	// from the earlier attempt and another empty commit would just add
	// noise.
	if out, err := r.git("rev-parse", "--verify", "HEAD"); err == nil && strings.TrimSpace(out) != "" {
		// Existing HEAD — this is a resume. Skip the initial commit.
		return nil
	}

	// Fresh repo: stage everything and make the initial empty commit.
	if out, err := r.git("add", "."); err != nil {
		r.failed = true
		return fmt.Errorf("git add (initial): %w\n%s", err, out)
	}
	msg := fmt.Sprintf("tracker: run %s started", r.runID)
	if out, err := r.git("commit", "--allow-empty", "-m", msg); err != nil {
		r.failed = true
		return fmt.Errorf("git commit (initial): %w\n%s", err, out)
	}
	return nil
}

// CommitNode stages all changes and creates a commit recording the node outcome.
// The commit message format is:
//
//	node(<nodeID>): <handler> outcome=<status>
//
//	duration: <duration>
//	edge_to: <edgeTo>  (if set)
//	tokens: <total> cost: $<cost>  (if Stats is non-nil)
//
// Returns the error on failure. The caller (emitGitCommit in engine_run.go)
// is responsible for emitting it as a PipelineEvent warning. On failure the
// repo is NOT marked failed=true, so subsequent node commits are still
// attempted — individual commit failures should not take down the whole run.
func (r *gitArtifactRepo) CommitNode(nodeID, handler, status string, entry *TraceEntry) error {
	if r.failed {
		return nil
	}

	// Stage all changes in the artifact dir.
	if _, err := r.git("add", "."); err != nil {
		// Non-fatal: log and continue.
		return fmt.Errorf("git add for node %q: %w", nodeID, err)
	}

	// Build commit message.
	var sb strings.Builder
	fmt.Fprintf(&sb, "node(%s): %s outcome=%s", nodeID, handler, status)
	if entry != nil {
		sb.WriteString("\n\n")
		fmt.Fprintf(&sb, "duration: %s\n", entry.Duration)
		if entry.EdgeTo != "" {
			fmt.Fprintf(&sb, "edge_to: %s\n", entry.EdgeTo)
		}
		if entry.Stats != nil {
			fmt.Fprintf(&sb, "tokens: %d cost: $%.6f\n",
				entry.Stats.TotalTokens, entry.Stats.CostUSD)
		}
	}
	msg := strings.TrimRight(sb.String(), "\n")

	if _, err := r.git("commit", "--allow-empty", "-m", msg); err != nil {
		// Non-fatal: log and continue.
		return fmt.Errorf("git commit for node %q: %w", nodeID, err)
	}
	return nil
}

// TagCheckpoint creates a lightweight git tag `checkpoint/<runID>/<nodeID>`
// pointing at HEAD. This enables checkpoint resume replay from a known snapshot
// (Layer 2 work). Returns nil on success or a non-fatal error on failure.
func (r *gitArtifactRepo) TagCheckpoint(nodeID string) error {
	if r.failed {
		return nil
	}
	tag := fmt.Sprintf("checkpoint/%s/%s", r.runID, nodeID)
	// Use -f to overwrite if the same node is tagged again (e.g. retry).
	if _, err := r.git("tag", "-f", tag); err != nil {
		return fmt.Errorf("git tag %q: %w", tag, err)
	}
	return nil
}

// git runs a git command in r.dir with a sanitized environment.
// Returns combined output and any error.
func (r *gitArtifactRepo) git(args ...string) (string, error) {
	cmdArgs := append([]string{"-C", r.dir}, args...)
	cmd := exec.Command("git", cmdArgs...) //nolint:gosec // controlled args
	cmd.Env = gitSafeEnv()
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// gitSafeEnv returns a copy of the current environment with sensitive variables
// stripped to avoid leaking credentials into the git subprocess.
// Mirrors the filterSensitiveEnv logic used by the tool handler.
func gitSafeEnv() []string {
	env := os.Environ()
	var filtered []string
	for _, e := range env {
		name := strings.ToUpper(strings.SplitN(e, "=", 2)[0])
		if gitEnvIsSafe(name) {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// gitEnvIsSafe returns true if the env var is safe to pass to git subprocesses.
func gitEnvIsSafe(name string) bool {
	for _, pattern := range []string{"_API_KEY", "_SECRET", "_TOKEN", "_PASSWORD"} {
		if strings.Contains(name, pattern) {
			return false
		}
	}
	return true
}
