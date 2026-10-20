// ABOUTME: TRACKER_CLAUDE_BIN (#719) names the claude binary for every claude-code use, fails hard
// ABOUTME: when broken (never falls back to PATH or to native), and runs always say which claude ran.
package handlers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/pipeline"
)

// twoClaudes puts a stale claude first on PATH and a current one elsewhere,
// each counting its runs: Joi's machine in #719.
func twoClaudes(t *testing.T, e *autoEnv) (stale, current string) {
	t.Helper()
	staleDir := filepath.Join(t.TempDir(), "stale")
	currentDir := filepath.Join(t.TempDir(), "current")
	stale = fakeAutoClaude(t, staleDir, e.countFile+".stale", "echo '2.1.269 (Claude Code)'")
	current = fakeAutoClaude(t, currentDir, e.countFile+".current", "echo '2.1.303 (Claude Code)'")
	withPathPrefix(t, staleDir)
	return stale, current
}

func TestClaudeBin_OverrideBeatsPath(t *testing.T) {
	e := newAutoEnv(t)
	_, current := twoClaudes(t, e)
	t.Setenv(claudeBinEnv, current)
	path, version, err := probeClaude(context.Background(), e.workDir, claudeProbeTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if path != current || version != "2.1.303 (Claude Code)" {
		t.Fatalf("probe = %s %q, want the TRACKER_CLAUDE_BIN binary", path, version)
	}
	if probeCount(t, e.countFile+".stale") != 0 {
		t.Fatal("the claude first on PATH must not run when TRACKER_CLAUDE_BIN is set")
	}
}

func TestClaudeBin_MustBeAbsolute(t *testing.T) {
	e := newAutoEnv(t)
	t.Setenv(claudeBinEnv, "claude")
	_, _, err := probeClaude(context.Background(), e.workDir, claudeProbeTimeout)
	if err == nil || !strings.Contains(err.Error(), claudeBinEnv) || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("err = %v, want a TRACKER_CLAUDE_BIN must-be-absolute error", err)
	}
}

func TestClaudeBin_BrokenOverrideNeverFallsBackToPath(t *testing.T) {
	e := newAutoEnv(t)
	twoClaudes(t, e)
	missing := filepath.Join(t.TempDir(), "nope", "claude")
	t.Setenv(claudeBinEnv, missing)
	_, err := NewClaudeCodeBackend()
	if err == nil || !strings.Contains(err.Error(), missing) || !strings.Contains(err.Error(), claudeBinEnv) {
		t.Fatalf("err = %v, want it to name %s and TRACKER_CLAUDE_BIN", err, missing)
	}
	if probeCount(t, e.countFile+".stale") != 0 {
		t.Fatal("a broken override must not fall back to the claude on PATH")
	}
}

func TestClaudeBin_AutoRefusesOnBrokenOverride(t *testing.T) {
	// Naming a binary is an explicit choice: auto must not quietly move to
	// the paid native client when it is broken.
	e := newAutoEnv(t)
	t.Setenv(claudeBinEnv, filepath.Join(t.TempDir(), "missing-claude"))
	_, err := e.newAutoHandler(t, true, pipeline.NewBackendResolver()).selectBackend(autoNode)
	var refused *refuseToStartError
	if !errors.As(err, &refused) || refused.Kind != refuseNoBackend || !strings.Contains(err.Error(), claudeBinEnv) {
		t.Fatalf("err = %v, want a no_backend refusal naming TRACKER_CLAUDE_BIN", err)
	}
}

func TestClaudeBin_OverrideInsideWorkdirIsTrusted(t *testing.T) {
	// The workdir refusal guards against a repo planting `claude` on PATH;
	// a shell-only override is the operator's own choice.
	e := newAutoEnv(t)
	inside := fakeAutoClaude(t, filepath.Join(e.workDir, "tools"), e.countFile, "echo '2.1.303 (Claude Code)'")
	t.Setenv(claudeBinEnv, inside)
	b, err := e.newAutoHandler(t, false, pipeline.NewBackendResolver()).selectBackend(autoNode)
	if err != nil {
		t.Fatal(err)
	}
	if cc, ok := b.(*ClaudeCodeBackend); !ok || cc.claudePath != inside {
		t.Fatalf("backend = %T %+v, want claude-code at the override", b, b)
	}
}

func TestClaudeCode_ExplicitBackendAnnouncesWhichClaude(t *testing.T) {
	e := newAutoEnv(t)
	_, current := twoClaudes(t, e)
	t.Setenv(claudeBinEnv, current)
	r := pipeline.NewBackendResolver()
	node := &pipeline.Node{ID: "Build", Shape: "box", Attrs: map[string]string{"backend": "claude-code"}}
	for i := 0; i < 2; i++ {
		if _, err := e.newAutoHandler(t, false, r).selectBackend(node); err != nil {
			t.Fatal(err)
		}
	}
	evs := e.resolvedEvents()
	want := "backend: claude-code (claude 2.1.303 (Claude Code) at " + current + ", from TRACKER_CLAUDE_BIN)"
	if len(evs) != 1 || evs[0].Message != want {
		t.Fatalf("announcements = %+v, want exactly one %q", evs, want)
	}
}

func TestClaudeCode_ExplicitAnnouncementDoesNotSwallowAuto(t *testing.T) {
	e := newAutoEnv(t)
	bin := filepath.Join(t.TempDir(), "bin")
	fakeAutoClaude(t, bin, e.countFile, "echo '2.1.303 (Claude Code)'")
	withPathPrefix(t, bin)
	r := pipeline.NewBackendResolver()
	explicit := &pipeline.Node{ID: "Build", Shape: "box", Attrs: map[string]string{"backend": "claude-code"}}
	if _, err := e.newAutoHandler(t, false, r).selectBackend(explicit); err != nil {
		t.Fatal(err)
	}
	if _, err := e.newAutoHandler(t, false, r).selectBackend(autoNode); err != nil {
		t.Fatal(err)
	}
	evs := e.resolvedEvents()
	if len(evs) != 2 || !strings.HasPrefix(evs[1].Message, "backend: auto → claude-code") {
		t.Fatalf("announcements = %+v, want the explicit line then auto's", evs)
	}
}

func TestClaudeCode_FatalErrorNamesTheBinary(t *testing.T) {
	// Joi's failure: an old CLI rejects the model. The error must point at
	// the binary, not only the model id.
	script := filepath.Join(t.TempDir(), "claude")
	body := "#!/bin/sh\necho 'API Error: 400 unrecognized_model claude-opus-5-5' >&2\nexit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	b := &ClaudeCodeBackend{claudePath: script, claudeVersion: "2.1.269 (Claude Code)"}
	_, err := b.Run(context.Background(), pipeline.AgentRunConfig{Prompt: "p"}, func(agent.Event) {})
	var fatal *backendFatalError
	if !errors.As(err, &fatal) {
		t.Fatalf("err = %v (%T), want a backendFatalError", err, err)
	}
	if want := "(claude 2.1.269 (Claude Code) at " + script + ")"; !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %q, want it to name the binary %q", err, want)
	}
}
