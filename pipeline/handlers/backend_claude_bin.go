// ABOUTME: Which claude binary the claude-code backend runs (#719): TRACKER_CLAUDE_BIN when set
// ABOUTME: (absolute, never falling back to PATH), else the PATH lookup — and naming it in errors.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

// claudeBinEnv names the claude binary for every claude-code use (explicit
// backend, backend: auto, autopilot, doctor). Shell-only in the env policy:
// it chooses what Tracker executes, so a project .env can never set it.
const claudeBinEnv = "TRACKER_CLAUDE_BIN"

// claudeSourcePath is the source label for a binary found on PATH.
const claudeSourcePath = "PATH"

// claudeBinary returns the claude to run and where it came from
// (TRACKER_CLAUDE_BIN or PATH). A set override must be an absolute path to
// an executable file; when it isn't, that is the error — PATH is not tried,
// so a wrong override never silently runs a different claude.
func claudeBinary() (path, source string, err error) {
	if p := os.Getenv(claudeBinEnv); p != "" {
		if err := checkClaudeOverride(p); err != nil {
			return "", "", err
		}
		return p, claudeBinEnv, nil
	}
	p, lerr := exec.LookPath("claude")
	if lerr != nil {
		return "", "", errors.New("claude CLI not found in PATH — install with: npm install -g @anthropic-ai/claude-code (or set " + claudeBinEnv + ")")
	}
	return p, claudeSourcePath, nil
}

// checkClaudeOverride validates TRACKER_CLAUDE_BIN: an absolute path to an
// executable regular file, keeping the os.Stat cause when there isn't one.
func checkClaudeOverride(p string) error {
	if !filepath.IsAbs(p) {
		return fmt.Errorf("%s=%q must be an absolute path to the claude binary", claudeBinEnv, p)
	}
	fi, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("%s=%s: %w", claudeBinEnv, p, err)
	}
	if fi.IsDir() || fi.Mode()&0o111 == 0 {
		return fmt.Errorf("%s=%s is not an executable file", claudeBinEnv, p)
	}
	return nil
}

// currentClaudeSource is where claude comes from for this process.
func currentClaudeSource() string {
	if os.Getenv(claudeBinEnv) != "" {
		return claudeBinEnv
	}
	return claudeSourcePath
}

// claudeDesc is "claude <version> at <path>" for errors and announcements.
func claudeDesc(path, version string) string {
	if version == "" {
		return "claude at " + path
	}
	return "claude " + version + " at " + path
}

// nameBinary appends which claude ran to a fatal claude-code error, so a
// failure such as an unrecognized model points at the binary (a stale CLI
// earlier on PATH) rather than only at the model id. It stays a
// backendFatalError; other errors pass through unchanged.
func (b *ClaudeCodeBackend) nameBinary(err error) error {
	var fatal *backendFatalError
	if err == nil || !errors.As(err, &fatal) {
		return err
	}
	return &backendFatalError{err: fmt.Errorf("%w (%s)", fatal.err, claudeDesc(b.claudePath, b.claudeVersion))}
}

// announceChosenClaude names the claude binary once per run when claude-code
// was chosen explicitly (node `backend: claude-code` or --backend
// claude-code); backend: auto announces its own resolution. Joi's #719: a
// stale claude earlier on PATH failed every node and nothing said which
// binary ran.
func (h *CodergenHandler) announceChosenClaude(node *pipeline.Node, b pipeline.AgentBackend, pctx *pipeline.PipelineContext) {
	cc, ok := b.(*ClaudeCodeBackend)
	if !ok || h.pipelineEmitter == nil {
		return
	}
	if node.AgentConfig(h.graphAttrs).Backend == "auto" && h.defaultBackendName == "" && !h.resumedAuto() {
		return // auto's own announcement already named the binary
	}
	if !h.resolver().ClaimExplicitAnnouncement() {
		return
	}
	d := pipeline.BackendResolvedDetail{
		Backend: "claude-code", Explicit: true,
		ClaudePath: cc.claudePath, ClaudeVersion: cc.claudeVersion, ClaudeSource: cc.claudeSource,
	}
	h.pipelineEmitter.HandlePipelineEvent(stampRunID(pipeline.PipelineEvent{
		Type: pipeline.EventBackendResolved, NodeID: node.ID, Timestamp: time.Now(),
		Message: pipeline.BackendResolvedMessage(d), BackendResolved: &d,
	}, pctx))
}

// resumedAuto reports whether backend: auto came from the checkpoint: its
// announcement says only "recorded in the checkpoint", so the claude it then
// probes and runs is named by the chosen-claude line too.
func (h *CodergenHandler) resumedAuto() bool {
	res, ok := h.resolver().Result()
	return ok && res.FromRecord
}

// ClaudeFound is one claude binary on PATH, with its `--version` (or why it
// didn't answer).
type ClaudeFound struct {
	Path    string
	Version string
	Err     error
}

// ClaudeSource reports where claude comes from for this process:
// TRACKER_CLAUDE_BIN or PATH.
func ClaudeSource() string { return currentClaudeSource() }

// ClaudesOnPath lists every executable named claude on PATH, in PATH order,
// once per resolved file — tracker doctor's #719 check for a stale claude
// earlier on PATH than the current one. Each is probed for its version only
// when there is more than one (a single claude is already probed by the
// caller, and cold starts are slow).
func ClaudesOnPath(ctx context.Context) []ClaudeFound {
	var found []ClaudeFound
	for _, p := range claudePathEntries() {
		found = append(found, ClaudeFound{Path: p})
	}
	if len(found) < 2 {
		return found
	}
	for i := range found {
		found[i].Version, found[i].Err = claudeVersionOf(ctx, found[i].Path, claudeProbeTimeout)
	}
	return found
}

// claudePathEntries is every executable claude on PATH, in order, once per
// resolved file (a symlink and its target count once).
func claudePathEntries() []string {
	var paths []string
	seen := map[string]bool{}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		p, ok := executableIn(dir, "claude")
		if !ok {
			continue
		}
		key := p
		if r, err := filepath.EvalSymlinks(p); err == nil {
			key = r
		}
		if !seen[key] {
			seen[key] = true
			paths = append(paths, p)
		}
	}
	return paths
}

// executableIn reports dir/name when it is an executable regular file.
func executableIn(dir, name string) (string, bool) {
	if dir == "" || !filepath.IsAbs(dir) {
		return "", false
	}
	p := filepath.Join(dir, name)
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() || fi.Mode()&0o111 == 0 {
		return "", false
	}
	return p, true
}
