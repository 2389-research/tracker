// ABOUTME: backend: auto — resolution order (--backend, claude, native client, refuse),
// ABOUTME: once per run across handlers, announcement, resume, planted-binary refusal.
package handlers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/pipeline"
)

// fakeAutoClaude writes an executable `claude` into dir that appends a line to
// countFile on every run and then behaves as body says.
func fakeAutoClaude(t *testing.T, dir, countFile, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "claude")
	script := "#!/bin/sh\necho x >> '" + countFile + "'\n" + body + "\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func probeCount(t *testing.T, countFile string) int {
	t.Helper()
	b, err := os.ReadFile(countFile)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "x")
}

type autoEnv struct {
	workDir, countFile string
	events             []pipeline.PipelineEvent
	mu                 sync.Mutex
}

// newAutoHandler builds a codergen handler for an auto node with the given
// native client (nil = none) and a shared resolver.
func (e *autoEnv) newAutoHandler(t *testing.T, client bool, r *pipeline.BackendResolver) *CodergenHandler {
	t.Helper()
	var h *CodergenHandler
	if client {
		h = NewCodergenHandler(nopCompleter{}, e.workDir)
	} else {
		h = NewCodergenHandler(nil, e.workDir)
	}
	h.backendResolver = r
	h.pipelineEmitter = pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.events = append(e.events, evt)
	})
	return h
}

func (e *autoEnv) resolvedEvents() []pipeline.PipelineEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []pipeline.PipelineEvent
	for _, evt := range e.events {
		if evt.Type == pipeline.EventBackendResolved {
			out = append(out, evt)
		}
	}
	return out
}

func newAutoEnv(t *testing.T) *autoEnv {
	t.Helper()
	root := t.TempDir()
	e := &autoEnv{workDir: filepath.Join(root, "work"), countFile: filepath.Join(root, "probes")}
	if err := os.MkdirAll(e.workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv(claudeBinEnv, "") // a developer's own override must not leak in
	return e
}

func withPathPrefix(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

var autoNode = &pipeline.Node{ID: "Answer", Shape: "box", Attrs: map[string]string{"backend": "auto"}}

func TestAuto_ClaudeWorks(t *testing.T) {
	e := newAutoEnv(t)
	bin := filepath.Join(t.TempDir(), "bin")
	fakeAutoClaude(t, bin, e.countFile, "echo '2.1.280 (Claude Code)'")
	withPathPrefix(t, bin)
	h := e.newAutoHandler(t, false, pipeline.NewBackendResolver())
	b, err := h.selectBackend(autoNode)
	if err != nil {
		t.Fatalf("selectBackend: %v", err)
	}
	cc, ok := b.(*ClaudeCodeBackend)
	if !ok || cc.claudePath != filepath.Join(bin, "claude") {
		t.Fatalf("backend = %T %+v, want ClaudeCodeBackend at the fake path", b, b)
	}
	evs := e.resolvedEvents()
	if len(evs) != 1 || !strings.Contains(evs[0].Message, "claude-code (claude 2.1.280 (Claude Code) at "+filepath.Join(bin, "claude")+"; no API key needed)") {
		t.Fatalf("announcement = %+v", evs)
	}
	if probeCount(t, e.countFile) != 1 {
		t.Fatalf("claude probed %d times, want 1", probeCount(t, e.countFile))
	}
}

func TestAuto_ClaudeVersionFails_NativeClient(t *testing.T) {
	e := newAutoEnv(t)
	bin := filepath.Join(t.TempDir(), "bin")
	fakeAutoClaude(t, bin, e.countFile, "exit 1")
	withPathPrefix(t, bin)
	b, err := e.newAutoHandler(t, true, pipeline.NewBackendResolver()).selectBackend(autoNode)
	if err != nil {
		t.Fatalf("selectBackend: %v", err)
	}
	if _, ok := b.(*NativeBackend); !ok {
		t.Fatalf("backend = %T, want *NativeBackend", b)
	}
}

func TestAuto_ClaudeTimesOut(t *testing.T) {
	old := claudeProbeTimeout
	claudeProbeTimeout = 100 * time.Millisecond
	defer func() { claudeProbeTimeout = old }()
	e := newAutoEnv(t)
	bin := filepath.Join(t.TempDir(), "bin")
	fakeAutoClaude(t, bin, e.countFile, "sleep 15")
	withPathPrefix(t, bin)

	start := time.Now()
	b, err := e.newAutoHandler(t, true, pipeline.NewBackendResolver()).selectBackend(autoNode)
	if time.Since(start) > 5*time.Second {
		t.Fatalf("probe did not honor the timeout (took %s)", time.Since(start))
	}
	if err != nil {
		t.Fatalf("selectBackend: %v", err)
	}
	if _, ok := b.(*NativeBackend); !ok {
		t.Fatalf("backend = %T, want native fallback", b)
	}
	evs := e.resolvedEvents()
	if len(evs) != 1 || !strings.Contains(evs[0].Message, "did not respond within") {
		t.Fatalf("fallback must be announced with the reason, got %+v", evs)
	}

	_, err = e.newAutoHandler(t, false, pipeline.NewBackendResolver()).selectBackend(autoNode)
	var refused *refuseToStartError
	if !errors.As(err, &refused) || refused.Kind != refuseNoBackend || !strings.Contains(err.Error(), "did not respond within") {
		t.Fatalf("no client + slow claude: err = %v, want no_backend refusal naming the timeout", err)
	}
}

func TestAuto_ClaudeInsideWorkdirRefused(t *testing.T) {
	e := newAutoEnv(t)
	planted := filepath.Join(e.workDir, "bin")
	fakeAutoClaude(t, planted, e.countFile, "echo '9.9.9'")
	withPathPrefix(t, planted)
	b, err := e.newAutoHandler(t, true, pipeline.NewBackendResolver()).selectBackend(autoNode)
	if err != nil {
		t.Fatalf("selectBackend: %v", err)
	}
	if _, ok := b.(*NativeBackend); !ok {
		t.Fatalf("a claude inside the workdir must not be used; backend = %T", b)
	}
	if probeCount(t, e.countFile) != 0 {
		t.Fatal("the planted claude must not even be executed")
	}
}

func TestAuto_NothingAvailable(t *testing.T) {
	e := newAutoEnv(t)
	_, err := e.newAutoHandler(t, false, pipeline.NewBackendResolver()).selectBackend(autoNode)
	var refused *refuseToStartError
	if !errors.As(err, &refused) || refused.Kind != refuseNoBackend {
		t.Fatalf("err = %v, want a no_backend refusal", err)
	}
	want := `no backend available: install Claude Code and run "claude /login" (no API key needed), or run "tracker setup" to add an API key`
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %q, want it to contain %q", err, want)
	}
}

func TestAuto_NothingAvailable_RoutesFailEdge(t *testing.T) {
	e := newAutoEnv(t)
	g := pipeline.NewGraph("auto_route")
	g.AddNode(&pipeline.Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&pipeline.Node{ID: "Answer", Shape: "box", Attrs: map[string]string{"backend": "auto", "prompt": "hi"}})
	g.AddNode(&pipeline.Node{ID: "Recover", Shape: "parallelogram", Attrs: map[string]string{"tool_command": "true"}})
	g.AddNode(&pipeline.Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&pipeline.Edge{From: "s", To: "Answer"})
	g.AddEdge(&pipeline.Edge{From: "Answer", To: "e", Condition: "ctx.outcome = success"})
	g.AddEdge(&pipeline.Edge{From: "Answer", To: "Recover", Condition: "ctx.outcome = fail"})
	g.AddEdge(&pipeline.Edge{From: "Recover", To: "e"})
	reg := NewDefaultRegistry(g, WithLLMClient(nil, e.workDir), WithExecEnvironment(exec.NewLocalEnvironment(e.workDir)), WithBackendResolver(pipeline.NewBackendResolver()))
	res, err := pipeline.NewEngine(g, reg).Run(context.Background())
	if err != nil {
		t.Fatalf("a refusal must route, not end the run: %v", err)
	}
	found := false
	for _, n := range res.CompletedNodes {
		found = found || n == "Recover"
	}
	if !found {
		t.Fatalf("Recover not reached; completed = %v", res.CompletedNodes)
	}
}

func TestAuto_GlobalBackendWins(t *testing.T) {
	e := newAutoEnv(t)
	bin := filepath.Join(t.TempDir(), "bin")
	fakeAutoClaude(t, bin, e.countFile, "echo 2.1.280")
	withPathPrefix(t, bin)
	h := e.newAutoHandler(t, true, pipeline.NewBackendResolver())
	h.defaultBackendName = "native"
	b, err := h.selectBackend(autoNode)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := b.(*NativeBackend); !ok || probeCount(t, e.countFile) != 0 || len(e.resolvedEvents()) != 0 {
		t.Fatalf("--backend native must win with no probe/announcement; backend=%T probes=%d", b, probeCount(t, e.countFile))
	}
}

func TestAuto_SharedResolverProbesOnceAcrossHandlers(t *testing.T) {
	e := newAutoEnv(t)
	bin := filepath.Join(t.TempDir(), "bin")
	fakeAutoClaude(t, bin, e.countFile, "echo 2.1.280")
	withPathPrefix(t, bin)
	r := pipeline.NewBackendResolver()
	// Three handlers stand in for the parent registry and two child
	// registries (parallel branches share the parent's; a subgraph builds its
	// own via NewRegistryFactory, sharing the resolver pointer).
	hs := []*CodergenHandler{e.newAutoHandler(t, false, r), e.newAutoHandler(t, false, r), e.newAutoHandler(t, false, r)}
	var wg sync.WaitGroup
	errs := make(chan error, 9)
	for i := 0; i < 9; i++ {
		wg.Add(1)
		go func(h *CodergenHandler) {
			defer wg.Done()
			if _, err := h.selectBackend(autoNode); err != nil {
				errs <- err
			}
		}(hs[i%3])
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("selectBackend: %v", err)
	}
	if n := probeCount(t, e.countFile); n != 1 {
		t.Fatalf("claude probed %d times across handlers, want 1", n)
	}
	if n := len(e.resolvedEvents()); n != 1 {
		t.Fatalf("%d announcements, want 1", n)
	}
}

func TestAuto_RecordedClaudeUnavailableOnResume(t *testing.T) {
	e := newAutoEnv(t)
	r := pipeline.NewBackendResolver()
	if err := r.Seed("claude-code"); err != nil {
		t.Fatal(err)
	}
	_, err := e.newAutoHandler(t, true, r).selectBackend(autoNode)
	var refused *refuseToStartError
	want := "this run resolved backend: auto to claude-code, which is no longer available; resume with --backend native to switch deliberately"
	if !errors.As(err, &refused) || refused.Kind != refuseBackendUnavailable || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want backend_unavailable containing %q", err, want)
	}
}

func TestAuto_GlobalBackendBeatsRecord(t *testing.T) {
	e := newAutoEnv(t)
	r := pipeline.NewBackendResolver()
	_ = r.Seed("claude-code")
	h := e.newAutoHandler(t, true, r)
	h.defaultBackendName = "native"
	b, err := h.selectBackend(autoNode)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := b.(*NativeBackend); !ok {
		t.Fatalf("--backend native must beat the recorded claude-code; got %T", b)
	}
}

func TestAuto_SignedOutHint(t *testing.T) {
	r := pipeline.NewBackendResolver()
	_, _ = r.Resolve(func() (pipeline.BackendResolution, error) {
		return pipeline.BackendResolution{Backend: "claude-code"}, nil
	})
	h := NewCodergenHandler(nil, t.TempDir())
	h.backendResolver = r
	authErr := &backendFatalError{err: errors.New("claude CLI failed (exit 1, outcome=fail): Invalid API key · Please run /login")}
	got := h.withAutoAuthHint(autoNode, authErr)
	hint := `this run chose claude-code automatically; run "claude /login", or use --backend native with an API key`
	if !strings.HasSuffix(got.Error(), hint) {
		t.Fatalf("err = %q, want suffix %q", got, hint)
	}
	var fatal *backendFatalError
	if !errors.As(got, &fatal) {
		t.Fatal("the wrapped error must stay a backendFatalError (still non-retryable)")
	}
	plain := &pipeline.Node{ID: "N", Attrs: map[string]string{"backend": "claude-code"}}
	if h.withAutoAuthHint(plain, authErr) != error(authErr) {
		t.Fatal("a node that chose claude-code explicitly gets no hint")
	}
}

func TestAuto_AnnouncementCarriesRunID(t *testing.T) {
	e := newAutoEnv(t)
	bin := filepath.Join(t.TempDir(), "bin")
	fakeAutoClaude(t, bin, e.countFile, "echo '2.1.280 (Claude Code)'")
	withPathPrefix(t, bin)
	h := e.newAutoHandler(t, false, pipeline.NewBackendResolver())
	pctx := pipeline.NewPipelineContext()
	pctx.SetInternal(pipeline.InternalKeyRunID, "run-abc")
	if _, _, err := h.selectBackendOrRefuse(autoNode, "p", pctx); err != nil {
		t.Fatal(err)
	}
	evs := e.resolvedEvents()
	if len(evs) != 1 || evs[0].RunID != "run-abc" {
		t.Fatalf("backend_resolved events = %+v, want one stamped run_id run-abc", evs)
	}
}

func TestAuto_RecordedClaudeInsideWorkdirRefusedOnResume(t *testing.T) {
	// On resume the recorded claude-code is re-checked with the same
	// containment rule: a claude newly planted inside the workdir is never run.
	e := newAutoEnv(t)
	planted := filepath.Join(e.workDir, "bin")
	fakeAutoClaude(t, planted, e.countFile, "echo '9.9.9'")
	withPathPrefix(t, planted)
	r := pipeline.NewBackendResolver()
	if err := r.Seed("claude-code"); err != nil {
		t.Fatal(err)
	}
	_, err := e.newAutoHandler(t, true, r).selectBackend(autoNode)
	var refused *refuseToStartError
	if !errors.As(err, &refused) || refused.Kind != refuseBackendUnavailable {
		t.Fatalf("err = %v, want backend_unavailable", err)
	}
	if probeCount(t, e.countFile) != 0 {
		t.Fatal("the planted claude must not be executed on resume")
	}
}

func TestPathInside_RelativeWorkdir(t *testing.T) {
	// A relative workdir ("." from the tracker process) still contains an
	// absolute path under it.
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !pathInside(filepath.Join(dir, "bin", "claude"), ".") {
		t.Fatal("an absolute path under a relative workdir must count as inside")
	}
	if pathInside(filepath.Join(t.TempDir(), "claude"), ".") {
		t.Fatal("a path elsewhere must not count as inside")
	}
}

func TestAuto_ClaudeVersionFailsIsAnnounced(t *testing.T) {
	// A broken claude install must be named, not silently swapped for a
	// paid API key.
	e := newAutoEnv(t)
	bin := filepath.Join(t.TempDir(), "bin")
	fakeAutoClaude(t, bin, e.countFile, "echo broken >&2; exit 3")
	withPathPrefix(t, bin)
	if _, err := e.newAutoHandler(t, true, pipeline.NewBackendResolver()).selectBackend(autoNode); err != nil {
		t.Fatal(err)
	}
	evs := e.resolvedEvents()
	want := "claude found at " + filepath.Join(bin, "claude") + " but `claude --version` failed"
	if len(evs) != 1 || !strings.Contains(evs[0].Message, want) {
		t.Fatalf("announcement = %+v, want it to contain %q", evs, want)
	}
}

func TestClaudeProbe_ExplicitBackendWaitsLonger(t *testing.T) {
	// A cold claude start that misses auto's bound must still work when the
	// user chose claude-code explicitly.
	e := newAutoEnv(t)
	bin := filepath.Join(t.TempDir(), "bin")
	fakeAutoClaude(t, bin, e.countFile, "sleep 2; echo '2.1.280 (Claude Code)'")
	withPathPrefix(t, bin)
	oldAuto, oldExplicit := claudeProbeTimeout, claudeExplicitProbeTimeout
	claudeProbeTimeout, claudeExplicitProbeTimeout = 1*time.Second, 10*time.Second
	t.Cleanup(func() { claudeProbeTimeout, claudeExplicitProbeTimeout = oldAuto, oldExplicit })
	if _, _, err := resolveClaudePathVersion(context.Background(), e.workDir); err == nil {
		t.Fatal("auto's probe should time out on a 2s start at a 1s bound")
	}
	if _, err := resolveClaudePath(); err != nil {
		t.Fatalf("explicit claude-code must wait for a cold start: %v", err)
	}
}
