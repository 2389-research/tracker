package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/2389-research/tracker/agent"
	execpkg "github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/pipeline"
)

func TestConfigureJail_NotSet_NoOp(t *testing.T) {
	env := execpkg.NewLocalEnvironment(t.TempDir())
	cfg := agent.SessionConfig{
		WorkingDir:       "./work",
		WritablePaths:    nil,
		WritablePathsSet: false,
		Backend:          "native",
	}
	enabled, err := configureJail(&cfg, env, "/home/user/run")
	if err != nil {
		t.Fatalf("configureJail = %v, want nil for unset", err)
	}
	if enabled {
		t.Error("configureJail reported enabled=true with WritablePathsSet=false")
	}
	if env.CommandWrapper != nil || env.WriteOpener != nil {
		t.Error("env hooks set when jail not enabled")
	}
}

func TestConfigureJail_SetButEmpty_FailsClosed(t *testing.T) {
	env := execpkg.NewLocalEnvironment(t.TempDir())
	cfg := agent.SessionConfig{
		WorkingDir:       "./work",
		WritablePaths:    nil,
		WritablePathsSet: true,
		Backend:          "native",
	}
	_, err := configureJail(&cfg, env, "/home/user/run")
	if err == nil {
		t.Fatal("configureJail with Set=true + empty paths = nil; want fail-closed")
	}
	// Validation should surface that the paths list is empty.
	if !strings.Contains(strings.ToLower(err.Error()), "empty") {
		t.Errorf("err = %v, want substring 'empty'", err)
	}
}

func TestConfigureJail_RefusesOnClaudeCode(t *testing.T) {
	env := execpkg.NewLocalEnvironment(t.TempDir())
	cfg := agent.SessionConfig{
		WorkingDir:       "./work",
		WritablePaths:    []string{"workspace/**"},
		WritablePathsSet: true,
		Backend:          "claude-code",
	}
	_, err := configureJail(&cfg, env, "/home/user/run")
	if err == nil {
		t.Fatal("configureJail with claude-code backend = nil; want refuse")
	}
	if !strings.Contains(err.Error(), "claude-code") {
		t.Errorf("err = %v, want message naming claude-code backend", err)
	}
}

func TestConfigureJail_RefusesOnAcp(t *testing.T) {
	env := execpkg.NewLocalEnvironment(t.TempDir())
	cfg := agent.SessionConfig{
		WorkingDir:       "./work",
		WritablePaths:    []string{"workspace/**"},
		WritablePathsSet: true,
		Backend:          "acp",
	}
	_, err := configureJail(&cfg, env, "/home/user/run")
	if err == nil {
		t.Fatal("configureJail with acp backend = nil; want refuse")
	}
	if !strings.Contains(err.Error(), "acp") {
		t.Errorf("err = %v, want message naming acp backend", err)
	}
}

func TestConfigureJail_RefusesOnUnknownBackend(t *testing.T) {
	// Unknown backend names fail-closed; safer to refuse than silently no-op
	// on a future backend that doesn't enforce.
	env := execpkg.NewLocalEnvironment(t.TempDir())
	cfg := agent.SessionConfig{
		WorkingDir:       "./work",
		WritablePaths:    []string{"workspace/**"},
		WritablePathsSet: true,
		Backend:          "future-backend-xyz",
	}
	_, err := configureJail(&cfg, env, "/home/user/run")
	if err == nil {
		t.Fatal("configureJail with unknown backend = nil; want refuse")
	}
	if !strings.Contains(err.Error(), "future-backend-xyz") {
		t.Errorf("err = %v, want message naming unknown backend", err)
	}
}

func TestConfigureJail_RefusesOnInvalidPaths(t *testing.T) {
	env := execpkg.NewLocalEnvironment(t.TempDir())
	cfg := agent.SessionConfig{
		WorkingDir:       "./work",
		WritablePaths:    []string{"/etc/**"},
		WritablePathsSet: true,
		Backend:          "native",
	}
	_, err := configureJail(&cfg, env, "/home/user/run")
	if !errors.Is(err, execpkg.ErrPathEscape) {
		t.Errorf("err = %v, want errors.Is(err, ErrPathEscape)", err)
	}
}

func TestConfigureJail_HappyPathWiresEnv(t *testing.T) {
	if probeErr := execpkg.ProbeLandlock(); probeErr != nil {
		t.Skipf("Landlock unavailable: %v", probeErr)
	}
	env := execpkg.NewLocalEnvironment(t.TempDir())
	cfg := agent.SessionConfig{
		WorkingDir:       "./work",
		WritablePaths:    []string{"workspace/**"},
		WritablePathsSet: true,
		Backend:          "native",
	}
	enabled, err := configureJail(&cfg, env, "/home/user/run")
	if err != nil {
		t.Fatalf("configureJail = %v, want nil", err)
	}
	if !enabled {
		t.Error("configureJail reported enabled=false on happy path")
	}
	if env.CommandWrapper == nil {
		t.Error("env.CommandWrapper not set")
	}
	if env.WriteOpener == nil {
		t.Error("env.WriteOpener not set")
	}
}

func TestConfigureJail_RefusesOnNoLandlock_SimulatedNonLinux(t *testing.T) {
	if probeErr := execpkg.ProbeLandlock(); probeErr == nil {
		t.Skip("Landlock available on this host; cannot exercise the no-Landlock refuse path")
	}
	env := execpkg.NewLocalEnvironment(t.TempDir())
	cfg := agent.SessionConfig{
		WorkingDir:       "./work",
		WritablePaths:    []string{"workspace/**"},
		WritablePathsSet: true,
		Backend:          "native",
	}
	_, err := configureJail(&cfg, env, "/home/user/run")
	if !errors.Is(err, execpkg.ErrLandlockUnavailable) {
		t.Errorf("err = %v, want errors.Is(err, ErrLandlockUnavailable)", err)
	}
}

func TestMatchWritablePath(t *testing.T) {
	cases := []struct {
		name    string
		relPath string
		globs   []string
		want    bool
	}{
		{
			name:    "exact match — directory glob",
			relPath: "workspace/foo.txt",
			globs:   []string{"workspace/**"},
			want:    true,
		},
		{
			name:    "deep match — directory glob",
			relPath: "workspace/a/b/c.txt",
			globs:   []string{"workspace/**"},
			want:    true,
		},
		{
			name:    "directory itself — directory glob",
			relPath: "workspace",
			globs:   []string{"workspace/**"},
			want:    true,
		},
		{
			name:    "exact file glob — matches",
			relPath: "workspace/out.md",
			globs:   []string{"workspace/out.md"},
			want:    true,
		},
		{
			name:    "exact file glob — does NOT match other file",
			relPath: "workspace/other.md",
			globs:   []string{"workspace/out.md"},
			want:    false,
		},
		{
			name:    "single-segment * glob — matches",
			relPath: "workspace/foo.md",
			globs:   []string{"workspace/*.md"},
			want:    true,
		},
		{
			name:    "single-segment * glob — does NOT match deeper path",
			relPath: "workspace/sub/foo.md",
			globs:   []string{"workspace/*.md"},
			want:    false,
		},
		{
			name:    "multiple globs — second matches",
			relPath: ".ai/sprints/2026/A.json",
			globs:   []string{"workspace/**", ".ai/sprints/**"},
			want:    true,
		},
		{
			name:    "no glob matches",
			relPath: "etc/passwd",
			globs:   []string{"workspace/**"},
			want:    false,
		},
		{
			name:    "** alone matches anything",
			relPath: "anything/at/all",
			globs:   []string{"**"},
			want:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matchWritablePath(tc.relPath, tc.globs)
			if got != tc.want {
				t.Errorf("matchWritablePath(%q, %v) = %v, want %v", tc.relPath, tc.globs, got, tc.want)
			}
		})
	}
}

// fakeBackendForGate is a tiny pipeline.AgentBackend stand-in for tests of
// refuseWritablePathsOnUnsupportedBackend. The gate dispatches on the
// concrete type via type assertion, so any non-*NativeBackend value
// exercises the refusal path. Run is never called from these tests.
type fakeBackendForGate struct{}

func (fakeBackendForGate) Run(_ context.Context, _ pipeline.AgentRunConfig, _ func(agent.Event)) (agent.SessionResult, error) {
	return agent.SessionResult{}, nil
}

func TestRefuseWritablePathsOnUnsupportedBackend(t *testing.T) {
	nodeWith := &pipeline.Node{Attrs: map[string]string{"writable_paths": "workspace/**"}}
	nodeWithout := &pipeline.Node{Attrs: map[string]string{}}
	native := &NativeBackend{}
	nonNative := fakeBackendForGate{}

	cases := []struct {
		name      string
		node      *pipeline.Node
		backend   pipeline.AgentBackend
		wantErr   bool
		wantInMsg string
	}{
		{name: "nil node is a no-op", node: nil, backend: native, wantErr: false},
		{name: "no writable_paths attr — pass", node: nodeWithout, backend: nonNative, wantErr: false},
		{name: "writable_paths + native — pass", node: nodeWith, backend: native, wantErr: false},
		{name: "writable_paths + non-native — refuse", node: nodeWith, backend: nonNative, wantErr: true, wantInMsg: "writable_paths refuses backend"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := refuseWritablePathsOnUnsupportedBackend(tc.node, tc.backend)
			if (err != nil) != tc.wantErr {
				t.Fatalf("refuseWritablePathsOnUnsupportedBackend = %v, wantErr=%v", err, tc.wantErr)
			}
			if tc.wantErr && !strings.Contains(err.Error(), tc.wantInMsg) {
				t.Errorf("err = %v, want substring %q", err, tc.wantInMsg)
			}
		})
	}
}

// TestHandleRunError_JailRefusalIsNonRetryableFail pins #642 (2): a
// writable_paths refuse-to-start (Landlock unavailable, bad globs, wrong
// backend) is a host/config condition — retrying can never change it. It must
// surface as a routable OutcomeFail (so fallback_target / `when ctx.outcome =
// fail` edges can escalate it once), not as OutcomeRetry.
func TestHandleRunError_JailRefusalIsNonRetryableFail(t *testing.T) {
	h := &CodergenHandler{}
	node := &pipeline.Node{ID: "FinalCommit"}
	refused := &jailRefusedError{err: fmt.Errorf("writable_paths requires Landlock: %w", execpkg.ErrLandlockUnavailable)}

	outcome, err := h.handleRunError(refused, node, "prompt", "", agent.SessionResult{}, nil, nil)
	if err != nil {
		t.Fatalf("handleRunError returned a hard error %v; want a routable OutcomeFail", err)
	}
	if outcome.Status != pipeline.OutcomeFail {
		t.Fatalf("outcome.Status = %q, want %q (a jail refusal is never worth retrying)", outcome.Status, pipeline.OutcomeFail)
	}
	got := outcome.ContextUpdates[pipeline.ContextKeyLastResponse]
	if !strings.Contains(got, "Landlock") || !strings.Contains(got, "FinalCommit") {
		t.Errorf("last_response = %q, want the actionable refusal message naming the node", got)
	}
}

// TestNativeBackend_JailRefusalIsTyped verifies the native backend wraps a
// resolveRunEnv refusal in jailRefusedError so handleRunError can classify it.
func TestNativeBackend_JailRefusalIsTyped(t *testing.T) {
	b := NewNativeBackend(nil, execpkg.NewLocalEnvironment(t.TempDir()))
	cfg := &agent.SessionConfig{
		WorkingDir:       ".",
		WritablePaths:    []string{"/abs/escape/**"}, // G1: absolute glob is refused on every host
		WritablePathsSet: true,
		Backend:          "native",
	}
	_, err := b.resolveRunEnv(cfg)
	if err == nil {
		t.Fatal("resolveRunEnv = nil; want a refusal")
	}
	var refused *jailRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("resolveRunEnv error %T (%v) is not a *jailRefusedError", err, err)
	}
}

// TestExecute_UnsupportedBackendRefusalIsRoutableFail pins that the
// dispatcher-layer gate (writable_paths + backend: claude-code / acp) yields
// the same non-retryable, routable OutcomeFail as the native-path gates
// (#642 review) — not a hard handler error.
func TestExecute_UnsupportedBackendRefusalIsRoutableFail(t *testing.T) {
	h := NewCodergenHandler(nil, t.TempDir())
	h.acpBackend = fakeBackendForGate{} // pre-seeded so no real ACP client is spawned
	node := &pipeline.Node{ID: "Jailed", Attrs: map[string]string{
		"writable_paths": "workspace/**",
		"backend":        "acp",
		"prompt":         "do the thing",
	}}
	outcome, err := h.Execute(context.Background(), node, pipeline.NewPipelineContext())
	if err != nil {
		t.Fatalf("Execute returned handler error %v; want a routable OutcomeFail", err)
	}
	if outcome.Status != pipeline.OutcomeFail {
		t.Fatalf("Status = %q, want %q", outcome.Status, pipeline.OutcomeFail)
	}
	if !strings.Contains(outcome.FailureReason, "writable_paths refuses backend") {
		t.Errorf("FailureReason = %q, want the backend refusal message", outcome.FailureReason)
	}
}
