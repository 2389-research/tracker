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

// claudeProbeTimeout bounds the `claude --version` probe (a cold npm start can
// be slow, but a hang must not stall the run).
var claudeProbeTimeout = 10 * time.Second

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
	if backend := node.AgentConfig(h.graphAttrs).Backend; backend != "" {
		switch backend {
		case "claude-code":
			return h.ensureClaudeCodeBackend()
		case "acp":
			return h.ensureACPBackend()
		case "native", "codergen":
			return h.ensureNativeBackend()
		case "auto":
			return h.resolveAutoBackend(node)
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
func (h *CodergenHandler) resolveAutoBackend(node *pipeline.Node) (pipeline.AgentBackend, error) {
	if h.defaultBackendName != "" {
		return h.defaultBackend()
	}
	r := h.resolver()
	res, err := r.Resolve(h.probeAutoBackend)
	if err != nil {
		return nil, err
	}
	if r.ClaimAnnouncement() {
		h.announceBackend(node, res)
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
		return pipeline.BackendResolution{Backend: "claude-code", Reason: "claude found", ClaudePath: path, ClaudeVersion: version}, nil
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
			return h.claudeCodeBackendAt(res.ClaudePath), nil
		}
		b, err := h.ensureClaudeCodeBackend()
		if err != nil {
			return nil, unavailableRecorded("claude-code", "native", err)
		}
		return b, nil
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
func (h *CodergenHandler) claudeCodeBackendAt(path string) pipeline.AgentBackend {
	h.claudeMu.Lock()
	defer h.claudeMu.Unlock()
	if h.claudeCodeBackend == nil {
		h.claudeCodeBackend = &ClaudeCodeBackend{claudePath: path}
	}
	return h.claudeCodeBackend
}

func (h *CodergenHandler) announceBackend(node *pipeline.Node, res pipeline.BackendResolution) {
	if h.pipelineEmitter == nil {
		return
	}
	d := pipeline.BackendResolvedDetail{
		Backend: res.Backend, Reason: res.Reason, ClaudePath: res.ClaudePath, ClaudeVersion: res.ClaudeVersion,
		FromRecord: res.FromRecord, NativeAvailable: res.Backend == "claude-code" && h.client != nil,
	}
	h.pipelineEmitter.HandlePipelineEvent(pipeline.PipelineEvent{
		Type: pipeline.EventBackendResolved, NodeID: node.ID, Timestamp: time.Now(),
		Message: pipeline.BackendResolvedMessage(d), BackendResolved: &d,
	})
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
type claudeTimeoutError struct{ path string }

func (e *claudeTimeoutError) Error() string {
	return fmt.Sprintf("claude found at %s but did not respond within %s", e.path, claudeProbeTimeout)
}

// resolveClaudePathVersion finds claude on PATH, refuses one inside workDir
// (when workDir is non-empty), and runs `claude --version` with a timeout,
// returning its trimmed output.
func resolveClaudePathVersion(ctx context.Context, workDir string) (string, string, error) {
	path, err := exec.LookPath("claude")
	if err != nil {
		return "", "", fmt.Errorf("claude CLI not found in PATH — install with: npm install -g @anthropic-ai/claude-code")
	}
	if workDir != "" && pathInside(path, workDir) {
		return "", "", errClaudeInWorkdir
	}
	ctx, cancel := context.WithTimeout(ctx, claudeProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Env = buildEnv()
	cmd.WaitDelay = 500 * time.Millisecond // a killed shell's children must not hold the pipe open
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return "", "", &claudeTimeoutError{path: path}
	}
	if err != nil {
		return "", "", fmt.Errorf("claude CLI version check failed: %w", err)
	}
	return path, strings.TrimSpace(string(out)), nil
}

// pathInside reports whether p resolves to a location inside dir.
func pathInside(p, dir string) bool {
	rp, err1 := filepath.EvalSymlinks(p)
	rd, err2 := filepath.EvalSymlinks(dir)
	if err1 != nil || err2 != nil {
		rp, rd = filepath.Clean(p), filepath.Clean(dir)
	}
	rel, err := filepath.Rel(rd, rp)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
