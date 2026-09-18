// ABOUTME: Contract tests for writable_paths_mode: prefer (#648) — pins spec
// ABOUTME: invariants C3–C6, C8–C11 from docs/superpowers/specs/2026-09-17-issue-648-writable-paths-prefer.md.
package handlers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/2389-research/tracker/agent"
	execpkg "github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

func preferCfg(globs ...string) agent.SessionConfig {
	return agent.SessionConfig{
		WorkingDir:        ".",
		WritablePaths:     globs,
		WritablePathsSet:  true,
		WritablePathsMode: pipeline.WritablePathsModePrefer,
		Backend:           "native",
	}
}

// requireLandlockAbsent skips on a host that CAN enforce the jail — the
// degrade path is unreachable there (C3 covers that host).
func requireLandlockAbsent(t *testing.T) {
	t.Helper()
	if err := execpkg.ProbeLandlock(); err == nil {
		t.Skip("Landlock available on this host; the prefer degrade path is unreachable")
	}
}

// C3: prefer + Landlock available ⇒ jailed exactly as require — all three
// hooks wired, no degrade. Linux ≥ 6.2 only.
func TestConfigureJail_C3_PreferJailsWhenLandlockAvailable(t *testing.T) {
	if err := execpkg.ProbeLandlock(); err != nil {
		t.Skipf("Landlock unavailable: %v", err)
	}
	anchor := t.TempDir()
	env := execpkg.NewLocalEnvironment(anchor)
	cfg := preferCfg("workspace/**")
	setup, err := setupJail(&cfg, env, anchor)
	if err != nil {
		t.Fatalf("setupJail = %v, want nil", err)
	}
	if !setup.Enabled || setup.Degraded != nil {
		t.Fatalf("setup = %+v, want Enabled=true Degraded=nil (prefer must jail exactly like require when the host can)", setup)
	}
	if env.CommandWrapper == nil || env.WriteOpener == nil || env.Remover == nil {
		t.Fatal("prefer on a Landlock host did not wire every hook")
	}
}

// C4 (AUTHORING): prefer still refuses a malformed glob — G1 precedes the
// host probe, so this holds on every OS. Rapid over the invalid-glob classes.
func TestConfigureJail_C4_PreferStillRefusesAuthoring_Rapid(t *testing.T) {
	cwd := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		env := execpkg.NewLocalEnvironment(cwd)
		cfg := preferCfg(drawInvalidGlob(rt, "g"))
		cfg.WorkingDir = "work"
		setup, err := setupJail(&cfg, env, cwd)
		if err == nil {
			rt.Fatalf("prefer accepted invalid glob %v, want refuse (authoring errors never degrade)", cfg.WritablePaths)
		}
		if setup.Enabled || setup.Degraded != nil {
			rt.Fatalf("setup = %+v on a G1 refusal", setup)
		}
		if env.CommandWrapper != nil || env.WriteOpener != nil || env.Remover != nil {
			rt.Fatal("env hooks wired despite G1 refusal under prefer")
		}
	})
}

// C4 (BACKEND): prefer still refuses claude-code / acp / unknown by name.
func TestConfigureJail_C4_PreferStillRefusesBackend_Rapid(t *testing.T) {
	cwd := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		backend := drawNonNativeBackend(rt)
		env := execpkg.NewLocalEnvironment(cwd)
		cfg := preferCfg(drawValidGlob(rt, "g"))
		cfg.WorkingDir = "work"
		cfg.Backend = backend
		setup, err := setupJail(&cfg, env, cwd)
		if err == nil {
			rt.Fatalf("prefer accepted backend %q, want refuse (out-of-process backends never degrade — #275)", backend)
		}
		if setup.Enabled || setup.Degraded != nil {
			rt.Fatalf("setup = %+v on a G2 refusal", setup)
		}
		if !strings.Contains(err.Error(), backend) {
			rt.Fatalf("err %v does not name backend %q", err, backend)
		}
	})
}

// C4 (BACKEND, dispatcher layer) + C11: refuseWritablePathsOnUnsupportedBackend
// is mode-agnostic — prefer does not reopen the #275 hole.
func TestRefuseWritablePathsOnUnsupportedBackend_C4_PreferMatrix(t *testing.T) {
	for _, mode := range []string{"", pipeline.WritablePathsModeRequire, pipeline.WritablePathsModePrefer} {
		node := &pipeline.Node{Attrs: map[string]string{"writable_paths": "workspace/**"}}
		if mode != "" {
			node.Attrs[pipeline.AttrWritablePathsMode] = mode
		}
		if err := refuseWritablePathsOnUnsupportedBackend(node, fakeBackendForGate{}); err == nil {
			t.Errorf("mode %q: non-native backend accepted at the dispatcher gate; #275 hole reopened", mode)
		}
		if err := refuseWritablePathsOnUnsupportedBackend(node, &NativeBackend{}); err != nil {
			t.Errorf("mode %q: native backend refused: %v", mode, err)
		}
	}
}

// C5 + C6 (unit): prefer on a Landlock-less host ⇒ setupJail returns a
// degrade (not an error), Bash is NOT wrapped, and the in-process hooks still
// enforce the glob policy — an out-of-glob write is refused and creates no
// directory; an in-glob write lands; an anchor escape is refused.
func TestConfigureJail_C6_DegradedInProcessWritesStayBounded(t *testing.T) {
	requireLandlockAbsent(t)
	anchor := t.TempDir()
	env := execpkg.NewLocalEnvironment(anchor)
	cfg := preferCfg("workspace/**")
	setup, err := setupJail(&cfg, env, anchor)
	if err != nil {
		t.Fatalf("setupJail = %v, want a degrade (not a refusal) under prefer", err)
	}
	if setup.Enabled {
		t.Fatal("setup.Enabled=true without Landlock")
	}
	if setup.Degraded == nil {
		t.Fatal("setup.Degraded=nil; prefer must report the degrade")
	}
	if setup.Degraded.Mode != pipeline.WritablePathsModePrefer || setup.Degraded.Reason == "" {
		t.Errorf("Degraded detail = %+v, want Mode=prefer and a non-empty Reason", setup.Degraded)
	}
	if got := setup.Degraded.DeclaredGlobs; len(got) != 1 || got[0] != "workspace/**" {
		t.Errorf("DeclaredGlobs = %v, want [workspace/**]", got)
	}
	if env.CommandWrapper != nil {
		t.Fatal("C5: CommandWrapper installed under degrade — Bash must be plainly unjailed, never wrapped through a __jail-exec that cannot apply Landlock")
	}
	if env.WriteOpener == nil || env.Remover == nil {
		t.Fatal("C6: in-process hooks NOT installed under degrade — Write/Edit/ApplyPatch must stay glob-bounded")
	}

	// In-glob write lands (through the LocalEnvironment seam, like the tools do).
	if err := env.WriteFile(context.Background(), "workspace/ok.txt", "hi"); err != nil {
		t.Fatalf("in-glob write refused under degrade: %v", err)
	}
	if _, err := os.Stat(filepath.Join(anchor, "workspace", "ok.txt")); err != nil {
		t.Fatalf("in-glob file not created: %v", err)
	}
	// Out-of-glob write refused, and the rejected write left no directory.
	err = env.WriteFile(context.Background(), "src/main.go", "package main")
	if !errors.Is(err, execpkg.ErrPathNotAllowed) {
		t.Fatalf("out-of-glob write = %v, want ErrPathNotAllowed", err)
	}
	if _, e := os.Stat(filepath.Join(anchor, "src")); !os.IsNotExist(e) {
		t.Fatalf("rejected write created %q (stat err=%v)", "src", e)
	}
	// Anchor escape refused at the opener (LocalEnvironment.safePath also
	// blocks it earlier; the opener is checked directly to pin its own policy).
	if _, err := env.WriteOpener(filepath.Join(filepath.Dir(anchor), "escape.txt"), 0o644); !errors.Is(err, execpkg.ErrPathEscape) {
		t.Fatalf("escape write = %v, want ErrPathEscape", err)
	}
	// Remover honours the same policy.
	if err := env.Remover(filepath.Join(anchor, "src", "x")); !errors.Is(err, execpkg.ErrPathNotAllowed) {
		t.Fatalf("out-of-glob remove = %v, want ErrPathNotAllowed", err)
	}
	if err := env.RemoveFile(context.Background(), "workspace/ok.txt"); err != nil {
		t.Fatalf("in-glob remove refused under degrade: %v", err)
	}
}

// C9: only the EXACT string "prefer" degrades. Every other value — "",
// "require", case/whitespace variants, random strings — takes the refuse path
// on a Landlock-less host.
func TestConfigureJail_C9_OnlyExactPreferDegrades_Rapid(t *testing.T) {
	requireLandlockAbsent(t)
	anchor := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		var mode string
		switch rapid.IntRange(0, 4).Draw(rt, "kind") {
		case 0:
			mode = ""
		case 1:
			mode = pipeline.WritablePathsModeRequire
		case 2:
			mode = "Prefer"
		case 3:
			mode = " prefer"
		default:
			mode = rapid.StringMatching(`[a-zA-Z_ ]{0,12}`).Filter(func(s string) bool { return s != pipeline.WritablePathsModePrefer }).Draw(rt, "mode")
		}
		env := execpkg.NewLocalEnvironment(anchor)
		cfg := preferCfg("workspace/**")
		cfg.WritablePathsMode = mode
		setup, err := setupJail(&cfg, env, anchor)
		if !errors.Is(err, execpkg.ErrLandlockUnavailable) {
			rt.Fatalf("mode %q: err = %v, want ErrLandlockUnavailable refusal (only exact \"prefer\" degrades)", mode, err)
		}
		if setup.Degraded != nil || setup.Enabled {
			rt.Fatalf("mode %q: setup = %+v on a refusal", mode, setup)
		}
		if env.CommandWrapper != nil || env.WriteOpener != nil || env.Remover != nil {
			rt.Fatalf("mode %q: hooks wired despite refusal", mode)
		}
	})
}

// C10: gate order. A prefer node with a malformed glob on a Landlock-less host
// refuses (G1 fires before G3) and never reports a degrade.
func TestConfigureJail_C10_AuthoringBeforeHost(t *testing.T) {
	requireLandlockAbsent(t)
	anchor := t.TempDir()
	env := execpkg.NewLocalEnvironment(anchor)
	cfg := preferCfg("/abs/**")
	setup, err := setupJail(&cfg, env, anchor)
	if err == nil {
		t.Fatal("prefer + bad glob on a Landlock-less host did not refuse")
	}
	if errors.Is(err, execpkg.ErrLandlockUnavailable) {
		t.Fatalf("refusal is the host probe (%v); G1 must fire first", err)
	}
	if setup.Degraded != nil {
		t.Fatalf("degrade reported (%+v) alongside an authoring refusal", setup.Degraded)
	}
}

// C5 (native backend): resolveRunEnv returns the degrade and the fresh env;
// Run emits exactly one EventJailDegraded agent event before the session.
func TestNativeBackend_C5_PreferDegradesOnHostWithoutLandlock(t *testing.T) {
	requireLandlockAbsent(t)
	root := t.TempDir()
	b := NewNativeBackend(&fakeCompleter{responseText: "done"}, execpkg.NewLocalEnvironment(root))
	cfg := preferCfg("workspace/**")
	env, degraded, err := b.resolveRunEnv(&cfg)
	if err != nil {
		t.Fatalf("resolveRunEnv = %v, want a degrade, not a refusal", err)
	}
	if degraded == nil {
		t.Fatal("resolveRunEnv reported no degrade under prefer without Landlock")
	}
	le, ok := env.(*execpkg.LocalEnvironment)
	if !ok || le == b.env {
		t.Fatalf("env = %T (shared=%v); want a fresh *LocalEnvironment so the policy hooks do not leak into the shared env", env, le == b.env)
	}
	if le.CommandWrapper != nil || le.WriteOpener == nil {
		t.Fatal("degraded env: want no CommandWrapper (Bash unjailed) and a WriteOpener (policy tier)")
	}

	// Run: exactly one jail_degraded agent event, before any session event.
	var seen []agent.EventType
	sc := agent.DefaultConfig()
	sc.WritablePaths = []string{"workspace/**"}
	sc.WritablePathsSet = true
	sc.WritablePathsMode = pipeline.WritablePathsModePrefer
	sc.Backend = "native"
	res, runErr := b.Run(context.Background(), pipeline.AgentRunConfig{Prompt: "go", Extra: &sc}, func(evt agent.Event) {
		seen = append(seen, evt.Type)
	})
	if runErr != nil {
		t.Fatalf("Run = %v; a degraded node must RUN", runErr)
	}
	if res.Turns == 0 {
		t.Fatal("session did not run")
	}
	count := 0
	first := -1
	for i, ty := range seen {
		if ty == EventJailDegraded {
			count++
			if first < 0 {
				first = i
			}
		}
	}
	if count != 1 {
		t.Fatalf("jail_degraded emitted %d times, want exactly 1 (events: %v)", count, seen)
	}
	if first != 0 {
		t.Fatalf("jail_degraded at index %d; must precede every session event (events: %v)", first, seen)
	}
}

// C5 + C7 + C8 (handler): a prefer node on a Landlock-less host RUNS to a
// success outcome, the handler emits exactly one pipeline jail_degraded event
// carrying the declared globs and an UNJAILED warning (never "sandboxed"),
// the internal agent event is not forwarded to the agent stream, and the
// trace stats carry jail=degraded.
func TestCodergen_C5_JailDegradedEventOnce(t *testing.T) {
	requireLandlockAbsent(t)
	client := &scriptedCompleter{responses: []*llm.Response{{
		Message:      llm.AssistantMessage("committed"),
		FinishReason: llm.FinishReason{Reason: "stop"},
		Usage:        llm.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}}}
	emitter := &stubPipelineEmitter{}
	var agentEvents []agent.Event
	workdir := t.TempDir()
	h := NewCodergenHandler(client, workdir, WithPipelineEmitter(emitter))
	h.env = execpkg.NewLocalEnvironment(workdir)
	h.eventHandler = agent.EventHandlerFunc(func(evt agent.Event) { agentEvents = append(agentEvents, evt) })
	node := &pipeline.Node{ID: "FinalCommit", Shape: "box", Handler: "codergen", Attrs: map[string]string{
		"prompt":                       "commit",
		"writable_paths":               ".git/**, .ai/**",
		pipeline.AttrWritablePathsMode: pipeline.WritablePathsModePrefer,
	}}
	outcome, err := h.Execute(context.Background(), node, pipeline.NewPipelineContext())
	if err != nil {
		t.Fatalf("Execute = %v; a prefer node must run unjailed, not fail", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Fatalf("outcome = %q (%s), want success", outcome.Status, outcome.FailureReason)
	}
	if outcome.Stats == nil || outcome.Stats.Jail != pipeline.JailDegraded {
		t.Fatalf("outcome.Stats.Jail = %+v, want %q (C7 trace marker)", outcome.Stats, pipeline.JailDegraded)
	}

	var degraded []pipeline.PipelineEvent
	for _, evt := range emitter.events {
		if evt.Type == pipeline.EventJailDegraded {
			degraded = append(degraded, evt)
		}
	}
	if len(degraded) != 1 {
		t.Fatalf("pipeline jail_degraded emitted %d times, want 1: %+v", len(degraded), emitter.events)
	}
	evt := degraded[0]
	if evt.NodeID != "FinalCommit" || evt.Timestamp.IsZero() || evt.Jail == nil {
		t.Fatalf("event = %+v, want NodeID=FinalCommit, timestamp, Jail detail", evt)
	}
	if got := evt.Jail.DeclaredGlobs; len(got) != 2 || got[0] != ".git/**" || got[1] != ".ai/**" {
		t.Errorf("DeclaredGlobs = %v, want [.git/** .ai/**]", got)
	}
	if evt.Jail.Mode != pipeline.WritablePathsModePrefer || evt.Jail.Reason == "" {
		t.Errorf("Jail detail = %+v, want Mode=prefer and a reason", evt.Jail)
	}
	assertUnjailedCopy(t, evt.Message)
	for _, ae := range agentEvents {
		if ae.Type == EventJailDegraded {
			t.Fatal("internal jail_degraded agent event leaked to the agent event stream (would double-log)")
		}
	}
}

// C8: the operator copy says UNJAILED and never calls the node sandboxed /
// jailed. Pins both the handler message and the suggestion-style wording.
func TestJailDegradedCopy_C8(t *testing.T) {
	msg := jailDegradedMessage("N", "landlock unavailable", []string{".git/**"})
	assertUnjailedCopy(t, msg)
	for _, want := range []string{"N", ".git/**", "landlock unavailable", "writable_paths_mode: require", "#648"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q: %s", want, msg)
		}
	}
}

func assertUnjailedCopy(t *testing.T, msg string) {
	t.Helper()
	if !strings.Contains(msg, "UNJAILED") {
		t.Errorf("degrade copy must say UNJAILED: %s", msg)
	}
	lower := strings.ToLower(msg)
	for _, banned := range []string{"is sandboxed", "is jailed", "sandboxed node"} {
		if strings.Contains(lower, banned) {
			t.Errorf("degrade copy describes the node as %q: %s", banned, msg)
		}
	}
}

// C2 (require unchanged): the same node under the default mode still refuses
// on a Landlock-less host with the #642 routable OutcomeFail — the degrade
// path is invisible to require.
func TestCodergen_C2_RequireStillRefusesWithoutLandlock(t *testing.T) {
	requireLandlockAbsent(t)
	emitter := &stubPipelineEmitter{}
	workdir := t.TempDir()
	h := NewCodergenHandler(&fakeCompleter{responseText: "x"}, workdir, WithPipelineEmitter(emitter))
	h.env = execpkg.NewLocalEnvironment(workdir)
	node := &pipeline.Node{ID: "FinalCommit", Shape: "box", Handler: "codergen", Attrs: map[string]string{
		"prompt":         "commit",
		"writable_paths": ".git/**",
	}}
	outcome, err := h.Execute(context.Background(), node, pipeline.NewPipelineContext())
	if err != nil {
		t.Fatalf("Execute = %v, want a routable OutcomeFail", err)
	}
	if outcome.Status != pipeline.OutcomeFail {
		t.Fatalf("outcome = %q, want fail (require refuses)", outcome.Status)
	}
	for _, evt := range emitter.events {
		if evt.Type == pipeline.EventJailDegraded {
			t.Fatal("require node emitted jail_degraded")
		}
	}
	if outcome.Stats != nil && outcome.Stats.Jail != "" {
		t.Fatalf("require refusal carries Stats.Jail=%q", outcome.Stats.Jail)
	}
}
