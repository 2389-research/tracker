// ABOUTME: Backend selection for codergen nodes, including `backend: auto`: resolve
// ABOUTME: claude-code or native once per run, announce it, and refuse when neither works.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

// claudeProbeTimeout bounds backend: auto's `claude --version` probe (a cold
// npm start can be slow, but a hang must not stall the run, and auto has a
// fallback). claudeExplicitProbeTimeout is the bound when the user chose
// claude-code: there is no fallback, so a slow cold start gets longer.
var (
	claudeProbeTimeout         = 10 * time.Second
	claudeExplicitProbeTimeout = 60 * time.Second
)

// errClaudeInWorkdir refuses a claude binary that resolves inside the run's
// working directory: a repo could plant one on an absolute PATH entry such as
// $PWD/bin or node_modules/.bin, and auto would run it for users who never
// chose claude-code.
var errClaudeInWorkdir = errors.New("claude on PATH resolves inside the working directory; not using it")

// autoResolverMu guards lazily creating a resolver for a handler built without
// one (library callers constructing a registry directly).
var autoResolverMu sync.Mutex

// selectBackend chooses the appropriate AgentBackend based on node attributes
// and global default settings. Priority:
//  1. Per-node "backend" attribute (highest — always wins over global flag);
//     "auto" resolves claude-code or native once per run (resolveAutoBackend)
//  2. Global --backend flag (fallback for nodes without explicit backend attr)
//  3. Default: native
//
// This means a node with "backend: native" always uses native even when the
// global --backend flag is "claude-code", enabling mixed-backend pipelines.
func (h *CodergenHandler) selectBackend(node *pipeline.Node) (pipeline.AgentBackend, error) {
	return h.selectBackendIn(node, nil)
}

// selectBackendIn is selectBackend with the node's pipeline context, which
// stamps the run ID on the backend: auto announcement (nil leaves it empty).
func (h *CodergenHandler) selectBackendIn(node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.AgentBackend, error) {
	b, err := h.chooseBackend(node, pctx)
	if err == nil {
		h.announceChosenClaude(node, b, pctx)
	}
	return b, err
}

// chooseBackend is the selection itself (see selectBackend for the order).
func (h *CodergenHandler) chooseBackend(node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.AgentBackend, error) {
	if backend := node.AgentConfig(h.graphAttrs).Backend; backend != "" {
		switch backend {
		case "claude-code":
			return h.ensureClaudeCodeBackend()
		case "acp":
			return h.ensureACPBackend()
		case "native", "codergen":
			return h.ensureNativeBackend()
		case "auto":
			return h.resolveAutoBackend(node, pctx)
		default:
			return nil, fmt.Errorf("unknown backend %q for node %q (valid: native, codergen, claude-code, acp, auto)", backend, node.ID)
		}
	}
	return h.defaultBackend()
}

// defaultBackend is the backend for a node with no backend attribute.
func (h *CodergenHandler) defaultBackend() (pipeline.AgentBackend, error) {
	switch h.defaultBackendName {
	case "claude-code":
		return h.ensureClaudeCodeBackend()
	case "acp":
		return h.ensureACPBackend()
	}
	return h.ensureNativeBackend()
}

// resolveAutoBackend handles `backend: auto`. An explicit --backend wins
// outright (no probe, nothing recorded); otherwise the run's resolver answers,
// probing on first use, and the first resolution is announced.
func (h *CodergenHandler) resolveAutoBackend(node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.AgentBackend, error) {
	if h.defaultBackendName != "" {
		return h.defaultBackend()
	}
	r := h.resolver()
	res, err := r.Resolve(h.probeAutoBackend)
	if err != nil {
		return nil, err
	}
	if r.ClaimAnnouncement() {
		h.announceBackend(node, res, pctx)
	}
	return h.dispatchResolved(res)
}

// resolver returns the run's resolver, creating a private one for a handler
// built without WithBackendResolver.
func (h *CodergenHandler) resolver() *pipeline.BackendResolver {
	autoResolverMu.Lock()
	defer autoResolverMu.Unlock()
	if h.backendResolver == nil {
		h.backendResolver = pipeline.NewBackendResolver()
	}
	return h.backendResolver
}

// probeAutoBackend runs the resolution order: claude (outside the workdir,
// answering --version in time), then the native client, else refuse.
func (h *CodergenHandler) probeAutoBackend() (pipeline.BackendResolution, error) {
	path, version, claudeErr := resolveClaudePathVersion(context.Background(), h.workingDir)
	if claudeErr == nil {
		return pipeline.BackendResolution{Backend: "claude-code", Reason: "claude found", ClaudePath: path, ClaudeVersion: version, ClaudeSource: currentClaudeSource()}, nil
	}
	if currentClaudeSource() == claudeBinEnv {
		// A named binary is an explicit choice: never fall back to native.
		return pipeline.BackendResolution{}, &refuseToStartError{Kind: refuseNoBackend, Err: fmt.Errorf(
			"no backend available: %s is set, but its claude is not usable: %v", claudeBinEnv, claudeErr)}
	}
	reason := h.nativeReason()
	if note := claudeProbeNote(claudeErr); note != "" {
		reason += "; " + note
	}
	if h.client != nil {
		return pipeline.BackendResolution{Backend: "native", Reason: reason}, nil
	}
	msg := `no backend available: install Claude Code and run "claude /login" (no API key needed), or run "tracker setup" to add an API key`
	if note := claudeProbeNote(claudeErr); note != "" {
		msg += " (" + note + ")"
	}
	return pipeline.BackendResolution{}, &refuseToStartError{Kind: refuseNoBackend, Err: errors.New(msg)}
}

// claudeProbeNote explains a claude that exists but was not usable; empty
// when claude simply isn't installed.
func claudeProbeNote(err error) string {
	var t *claudeTimeoutError
	switch {
	case errors.As(err, &t):
		return t.Error()
	case errors.Is(err, errClaudeInWorkdir):
		return err.Error()
	}
	var v *claudeVersionError
	if errors.As(err, &v) {
		return v.Error()
	}
	return ""
}

func (h *CodergenHandler) nativeReason() string {
	if p, ok := h.client.(interface{ DefaultProvider() string }); ok {
		return "provider " + p.DefaultProvider()
	}
	return "a configured LLM client"
}

// dispatchResolved returns the backend for a resolution. A recorded
// (resumed) value is re-checked for availability and refuses rather than
// switching silently.
func (h *CodergenHandler) dispatchResolved(res pipeline.BackendResolution) (pipeline.AgentBackend, error) {
	if res.Backend == "claude-code" {
		if !res.FromRecord {
			return h.claudeCodeBackendAt(res.ClaudePath, res.ClaudeVersion), nil
		}
		// Re-check with the same rule a fresh resolution uses, workdir
		// containment included, so a claude planted since never runs.
		path, version, err := resolveClaudePathVersion(context.Background(), h.workingDir)
		if err != nil {
			return nil, unavailableRecorded("claude-code", "native", err)
		}
		return h.claudeCodeBackendAt(path, version), nil
	}
	if h.client == nil && res.FromRecord {
		return nil, unavailableRecorded("native", "claude-code", errors.New("no LLM client configured"))
	}
	return h.ensureNativeBackend()
}

func unavailableRecorded(backend, other string, cause error) error {
	return &refuseToStartError{Kind: refuseBackendUnavailable, Err: fmt.Errorf(
		"this run resolved backend: auto to %s, which is no longer available; resume with --backend %s to switch deliberately (%v)",
		backend, other, cause)}
}

// claudeCodeBackendAt builds the claude-code backend from an already-probed
// path, so a fresh auto resolution never probes claude twice.
func (h *CodergenHandler) claudeCodeBackendAt(path, version string) pipeline.AgentBackend {
	h.claudeMu.Lock()
	defer h.claudeMu.Unlock()
	if h.claudeCodeBackend == nil {
		h.claudeCodeBackend = &ClaudeCodeBackend{claudePath: path, claudeVersion: version, claudeSource: currentClaudeSource()}
	}
	return h.claudeCodeBackend
}

func (h *CodergenHandler) announceBackend(node *pipeline.Node, res pipeline.BackendResolution, pctx *pipeline.PipelineContext) {
	if h.pipelineEmitter == nil {
		return
	}
	d := pipeline.BackendResolvedDetail{
		Backend: res.Backend, Reason: res.Reason, ClaudePath: res.ClaudePath, ClaudeVersion: res.ClaudeVersion,
		FromRecord: res.FromRecord, NativeAvailable: res.Backend == "claude-code" && h.client != nil,
		ClaudeSource: res.ClaudeSource,
	}
	h.pipelineEmitter.HandlePipelineEvent(stampRunID(pipeline.PipelineEvent{
		Type: pipeline.EventBackendResolved, NodeID: node.ID, Timestamp: time.Now(),
		Message: pipeline.BackendResolvedMessage(d), BackendResolved: &d,
	}, pctx))
}

// withAutoAuthHint appends the signed-out fix to a claude-code auth failure on
// a node whose backend came from auto. It stays a backendFatalError (still
// non-retryable).
func (h *CodergenHandler) withAutoAuthHint(node *pipeline.Node, err error) error {
	var fatal *backendFatalError
	if node.AgentConfig(h.graphAttrs).Backend != "auto" || !errors.As(err, &fatal) || !isAuthError(strings.ToLower(err.Error())) {
		return err
	}
	if res, ok := h.resolver().Result(); !ok || res.Backend != "claude-code" {
		return err
	}
	return &backendFatalError{err: fmt.Errorf(`%w; this run chose claude-code automatically; run "claude /login", or use --backend native with an API key`, fatal.err)}
}

// claudeTimeoutError is a claude that exists but did not answer --version.
type claudeTimeoutError struct {
	path    string
	timeout time.Duration
}

func (e *claudeTimeoutError) Error() string {
	return fmt.Sprintf("claude found at %s but did not respond within %s", e.path, e.timeout)
}

// claudeVersionError is a claude that exists but whose --version failed (a
// broken install): named, so auto never moves to an API key silently.
type claudeVersionError struct {
	path string
	err  error
}

func (e *claudeVersionError) Error() string {
	return fmt.Sprintf("claude found at %s but `claude --version` failed: %v", e.path, e.err)
}

func (e *claudeVersionError) Unwrap() error { return e.err }

// resolveClaudePathVersion finds claude on PATH, refuses one inside workDir
// (when workDir is non-empty), and runs `claude --version` with a timeout,
// returning its trimmed output.
func resolveClaudePathVersion(ctx context.Context, workDir string) (string, string, error) {
	return probeClaude(ctx, workDir, claudeProbeTimeout)
}

// probeClaude is resolveClaudePathVersion with an explicit --version bound.
// The workdir refusal applies only to a claude found on PATH: a planted
// binary is the threat it guards, and TRACKER_CLAUDE_BIN is the operator's
// own shell-only choice.
func probeClaude(ctx context.Context, workDir string, timeout time.Duration) (string, string, error) {
	path, source, err := claudeBinary()
	if err != nil {
		return "", "", err
	}
	if source == claudeSourcePath && workDir != "" && pathInside(path, workDir) {
		return "", "", errClaudeInWorkdir
	}
	version, err := claudeVersionOf(ctx, path, timeout)
	if err != nil {
		return "", "", err
	}
	return path, version, nil
}

// claudeVersionOf runs `<path> --version` with the claude-code launch
// environment and a timeout.
func claudeVersionOf(ctx context.Context, path string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Env = buildEnv()
	cmd.WaitDelay = 500 * time.Millisecond // a killed shell's children must not hold the pipe open
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return "", &claudeTimeoutError{path: path, timeout: timeout}
	}
	if err != nil {
		return "", &claudeVersionError{path: path, err: err}
	}
	return strings.TrimSpace(string(out)), nil
}

// pathInside reports whether p resolves to a location inside dir. Both are
// made absolute first (a relative dir such as "." against an absolute PATH
// entry would otherwise fail filepath.Rel); if that fails it reports true,
// so the caller refuses rather than runs the binary.
func pathInside(p, dir string) bool {
	ap, err1 := filepath.Abs(p)
	ad, err2 := filepath.Abs(dir)
	if err1 != nil || err2 != nil {
		return true
	}
	rp, err1 := filepath.EvalSymlinks(ap)
	rd, err2 := filepath.EvalSymlinks(ad)
	if err1 != nil || err2 != nil {
		rp, rd = ap, ad
	}
	rel, err := filepath.Rel(rd, rp)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ClaudeAvailable reports whether backend: auto would choose claude-code in
// workDir: claude on PATH, outside workDir, answering --version in time.
// tracker doctor uses it to judge whether a pipeline needs an API key.
func ClaudeAvailable(ctx context.Context, workDir string) (path, version string, ok bool) {
	p, v, err := ClaudeStatus(ctx, workDir)
	return p, v, err == nil
}

// ClaudeStatus is ClaudeAvailable with the reason claude isn't usable — a
// broken TRACKER_CLAUDE_BIN included, which the run refuses (#719).
func ClaudeStatus(ctx context.Context, workDir string) (path, version string, err error) {
	return resolveClaudePathVersion(ctx, workDir)
}
