// ABOUTME: Non-destructive working-tree WIP preservation — on a terminal node
// ABOUTME: failure, snapshot the project's uncommitted code to a recoverable ref (#488).
package pipeline

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// WithWorkDir sets the project working directory. When set and the directory is
// a git repo, the engine preserves the working tree's uncommitted work into a
// recoverable ref on a terminal node failure (#488) — non-destructively (the
// tree is left as-is). Empty disables working-tree preservation.
func WithWorkDir(dir string) EngineOption {
	return func(e *Engine) {
		e.workDir = dir
	}
}

// preserveWorkingTreeWIP captures the project working tree's uncommitted work
// (the milestone's in-flight CODE — tracked modifications AND untracked new
// files) into a recoverable git ref when a node fails terminally, so a resume or
// a human can restore it instead of redoing it from scratch (#488).
//
// It is NON-DESTRUCTIVE: it stages into a throwaway index (GIT_INDEX_FILE) and
// writes a commit with commit-tree, never touching the real index or working
// tree — the work also stays in place for an in-workdir resume; the ref is the
// durable safety net (recover with `git checkout <ref> -- .` or diff against it).
//
// No-ops (emitting nothing) when workDir is unset, isn't a git work tree, or the
// tree matches HEAD (nothing uncommitted). Best-effort: a git failure emits a
// soft warning and never blocks the halt.
func (e *Engine) preserveWorkingTreeWIP(s *runState, nodeID string) {
	if e.workDir == "" || !isGitWorkTree(e.workDir) {
		return
	}
	ref, ok, err := snapshotWorkingTree(e.workDir, s.runID, nodeID)
	switch {
	case err != nil:
		e.emitWIP(s.runID, nodeID, fmt.Sprintf(
			"couldn't snapshot in-flight work for failed node %q: %v — the work still remains in your working tree.", nodeID, err))
	case !ok:
		// Clean tree — nothing uncommitted to preserve; say nothing.
	default:
		e.emitWIP(s.runID, nodeID, fmt.Sprintf(
			"preserved in-flight changes from failed node %q — your working tree is untouched, and a snapshot is saved at %s (inspect with `git -C %s show %s`, restore with `git -C %s checkout %s -- .`).",
			nodeID, ref, e.workDir, ref, e.workDir, ref))
	}
}

func (e *Engine) emitWIP(runID, nodeID, msg string) {
	e.emit(PipelineEvent{Type: EventWarning, Timestamp: time.Now(), RunID: runID, NodeID: nodeID, Message: msg})
}

// snapshotWorkingTree writes a commit capturing the whole working tree into
// refs/tracker/wip/<runID>/<nodeID>, returning (ref, preserved?, err). preserved
// is false when the working tree already matches HEAD.
func snapshotWorkingTree(workDir, runID, nodeID string) (string, bool, error) {
	idx, err := os.CreateTemp("", "tracker-wip-index-*")
	if err != nil {
		return "", false, fmt.Errorf("temp index: %w", err)
	}
	idxPath := idx.Name()
	idx.Close()
	os.Remove(idxPath) // git wants to create it fresh; we just need the path reserved
	defer os.Remove(idxPath)

	env := append(gitSafeEnv(), "GIT_INDEX_FILE="+idxPath)
	// Stage the entire working tree (tracked mods + untracked new files, honoring
	// .gitignore) into the throwaway index, then write its tree.
	if out, err := runGitDir(workDir, env, "add", "-A"); err != nil {
		return "", false, fmt.Errorf("stage working tree: %v (%s)", err, strings.TrimSpace(out))
	}
	tree, err := runGitDir(workDir, env, "write-tree")
	if err != nil {
		return "", false, fmt.Errorf("write-tree: %v (%s)", err, strings.TrimSpace(tree))
	}
	tree = strings.TrimSpace(tree)

	// Nothing uncommitted → the working tree matches HEAD's tree.
	if headTree, err := runGitDir(workDir, gitSafeEnv(), "rev-parse", "HEAD^{tree}"); err == nil {
		if strings.TrimSpace(headTree) == tree {
			return "", false, nil
		}
	}

	sha, err := commitTree(workDir, tree, runID, nodeID)
	if err != nil {
		return "", false, err
	}
	ref := fmt.Sprintf("refs/tracker/wip/%s/%s", runID, nodeID)
	if out, err := runGitDir(workDir, gitSafeEnv(), "update-ref", ref, sha); err != nil {
		return "", false, fmt.Errorf("name snapshot %q: %v (%s) — recoverable via `git fsck --lost-found`", ref, err, strings.TrimSpace(out))
	}
	return ref, true, nil
}

// commitTree writes a commit for tree, parented on HEAD when it exists (an unborn
// HEAD yields a parentless snapshot). Uses a fixed tracker identity so a repo
// without a configured git user still commits.
func commitTree(workDir, tree, runID, nodeID string) (string, error) {
	env := append(gitSafeEnv(),
		"GIT_AUTHOR_NAME=tracker", "GIT_AUTHOR_EMAIL=tracker@local",
		"GIT_COMMITTER_NAME=tracker", "GIT_COMMITTER_EMAIL=tracker@local")
	args := []string{"commit-tree", tree, "-m", fmt.Sprintf("wip(%s): preserved in-flight work of failed node %q", runID, nodeID)}
	if head, err := runGitDir(workDir, gitSafeEnv(), "rev-parse", "--verify", "-q", "HEAD"); err == nil && strings.TrimSpace(head) != "" {
		args = append(args, "-p", strings.TrimSpace(head))
	}
	sha, err := runGitDir(workDir, env, args...)
	if err != nil {
		return "", fmt.Errorf("commit-tree: %v (%s)", err, strings.TrimSpace(sha))
	}
	return strings.TrimSpace(sha), nil
}

// isGitWorkTree reports whether dir is inside a git work tree.
func isGitWorkTree(dir string) bool {
	out, err := runGitDir(dir, GitProbeEnv(), "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// runGitDir runs `git -C dir args...` with the given env, capturing combined output.
func runGitDir(dir string, env []string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...) //nolint:gosec // controlled args
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}
