// ABOUTME: tracker doctor checks the backends a pipeline needs (#716): no provider-key
// ABOUTME: failure when no node needs native, claude required when a node needs it.
package tracker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// doctorEnv clears provider credentials and puts only the given dirs (plus
// the system bin dirs) on PATH.
func doctorEnv(t *testing.T, pathDirs ...string) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, k := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "OPENAI_COMPAT_API_KEY", "TRACKER_OPENAI_AUTH", "TRACKER_GATEWAY_URL", "TRACKER_GATEWAY_KIND"} {
		t.Setenv(k, "")
	}
	t.Setenv("PATH", strings.Join(append(pathDirs, "/usr/bin", "/bin"), string(os.PathListSeparator)))
	t.Setenv("TRACKER_CLAUDE_BIN", "") // a developer's own override must not leak in
	return t.TempDir()
}

func fakeClaudeDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\necho '2.1.302 (Claude Code)'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func doctorGraph(backends ...string) *pipeline.Graph {
	g := pipeline.NewGraph("doc")
	for i, b := range backends {
		n := &pipeline.Node{ID: string(rune('A' + i)), Shape: "box", Handler: "codergen", Attrs: map[string]string{}}
		if b != "" {
			n.Attrs["backend"] = b
		}
		g.AddNode(n)
	}
	return g
}

func doctorCheck(t *testing.T, r *DoctorReport, name string) CheckResult {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %q check in report", name)
	return CheckResult{}
}

func runDoctor(t *testing.T, cfg DoctorConfig, opts ...DoctorOption) *DoctorReport {
	t.Helper()
	r, err := Doctor(context.Background(), cfg, opts...)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	return r
}

func TestDoctor_AllClaudeCodePipelineNeedsNoKey(t *testing.T) {
	wd := doctorEnv(t, fakeClaudeDir(t))
	r := runDoctor(t, DoctorConfig{WorkDir: wd}, WithDoctorGraphs(doctorGraph("claude-code", "claude-code"), nil))
	c := doctorCheck(t, r, "LLM Providers")
	if c.Status != CheckStatusOK || !strings.Contains(c.Message, "not needed by this pipeline (runs on Claude Code)") {
		t.Fatalf("providers check = %+v", c)
	}
}

func TestDoctor_AutoWithClaudeNeedsNoKey(t *testing.T) {
	wd := doctorEnv(t, fakeClaudeDir(t))
	r := runDoctor(t, DoctorConfig{WorkDir: wd}, WithDoctorGraphs(doctorGraph("auto"), nil))
	if c := doctorCheck(t, r, "LLM Providers"); c.Status != CheckStatusOK {
		t.Fatalf("auto with claude available: providers check = %+v, want OK", c)
	}
}

func TestDoctor_AutoWithoutClaudeChecksKeysWithHint(t *testing.T) {
	wd := doctorEnv(t)
	r := runDoctor(t, DoctorConfig{WorkDir: wd}, WithDoctorGraphs(doctorGraph("auto"), nil))
	c := doctorCheck(t, r, "LLM Providers")
	if c.Status != CheckStatusError || !strings.Contains(c.Hint, "Claude Code") {
		t.Fatalf("auto without claude and no keys: providers check = %+v, want error with a Claude Code hint", c)
	}
}

func TestDoctor_MixedNativeUnchanged(t *testing.T) {
	wd := doctorEnv(t, fakeClaudeDir(t))
	r := runDoctor(t, DoctorConfig{WorkDir: wd}, WithDoctorGraphs(doctorGraph("claude-code", "native"), nil))
	if c := doctorCheck(t, r, "LLM Providers"); c.Status != CheckStatusError {
		t.Fatalf("a native node with no keys: providers check = %+v, want error", c)
	}
}

func TestDoctor_BackendFlagClaudeCodeNoPipeline(t *testing.T) {
	wd := doctorEnv(t, fakeClaudeDir(t))
	r := runDoctor(t, DoctorConfig{WorkDir: wd, Backend: "claude-code"})
	if c := doctorCheck(t, r, "LLM Providers"); c.Status != CheckStatusOK {
		t.Fatalf("doctor --backend claude-code, no pipeline: providers check = %+v, want OK", c)
	}
}

func TestDoctor_Issue716Repro(t *testing.T) {
	wd := doctorEnv(t, fakeClaudeDir(t))
	dip := filepath.Join(wd, "pipeline.dip")
	src := "workflow Probe\n  goal: \"probe\"\n  start: Start\n  exit: Done\n\n  agent Start\n    label: Start\n\n  agent Implement\n    model: claude-opus-5-5\n    prompt: \"implement\"\n\n  tool Check\n    command: true\n\n  agent Done\n    label: Done\n\n  edges\n    Start -> Implement\n    Implement -> Check\n    Check -> Done\n"
	if err := os.WriteFile(dip, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	r := runDoctor(t, DoctorConfig{WorkDir: wd, Backend: "claude-code", PipelineFile: dip})
	if c := doctorCheck(t, r, "LLM Providers"); c.Status == CheckStatusError {
		t.Fatalf("#716: --backend claude-code with no keys must not fail the providers check: %+v", c)
	}
}

func TestDoctor_ClaudeRequiredWhenNodeNeedsIt(t *testing.T) {
	wd := doctorEnv(t)
	r := runDoctor(t, DoctorConfig{WorkDir: wd}, WithDoctorGraphs(doctorGraph("claude-code"), nil))
	if c := doctorCheck(t, r, "Optional Binaries"); c.Status != CheckStatusError {
		t.Fatalf("a claude-code node with no claude on PATH: binaries check = %+v, want error", c)
	}
}

func TestDoctor_DippinMissingIsWarning(t *testing.T) {
	wd := doctorEnv(t)
	r := runDoctor(t, DoctorConfig{WorkDir: wd})
	c := doctorCheck(t, r, "Dippin Language")
	if c.Status != CheckStatusWarn || !strings.Contains(c.Message+c.Hint, "only needed to lint or edit .dip files") {
		t.Fatalf("dippin missing: check = %+v, want a warning saying it is only needed for authoring", c)
	}
}

func TestDoctor_ExplicitNativeBeatsAuto(t *testing.T) {
	// --backend native wins over backend: auto at run time, so doctor must
	// require a key even when claude is available.
	wd := doctorEnv(t, fakeClaudeDir(t))
	r := runDoctor(t, DoctorConfig{WorkDir: wd, Backend: "native"}, WithDoctorGraphs(doctorGraph("auto"), nil))
	if c := doctorCheck(t, r, "LLM Providers"); c.Status != CheckStatusError {
		t.Fatalf("--backend native with auto nodes and no key: providers check = %+v, want error", c)
	}
}

func TestDoctor_ExplicitClaudeCodeBeatsAutoAndNeedsClaude(t *testing.T) {
	wd := doctorEnv(t)
	r := runDoctor(t, DoctorConfig{WorkDir: wd, Backend: "claude-code"}, WithDoctorGraphs(doctorGraph("auto"), nil))
	if c := doctorCheck(t, r, "Optional Binaries"); c.Status != CheckStatusError {
		t.Fatalf("--backend claude-code with auto nodes and no claude: binaries check = %+v, want error", c)
	}
}

func TestDoctor_PipelineFileWithUnseenSubgraphsChecksKeys(t *testing.T) {
	// Without WithDoctorGraphs, Doctor can't see subgraph children; a pipeline
	// that references one is assumed to need native rather than passing.
	wd := doctorEnv(t, fakeClaudeDir(t))
	dip := filepath.Join(wd, "parent.dip")
	src := "workflow Parent\n  goal: \"p\"\n  start: Start\n  exit: Done\n\n  agent Start\n    label: Start\n\n  agent Work\n    backend: claude-code\n    prompt: \"work\"\n\n  subgraph Child\n    ref: child.dip\n\n  agent Done\n    label: Done\n\n  edges\n    Start -> Work\n    Work -> Child\n    Child -> Done\n"
	if err := os.WriteFile(dip, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	r := runDoctor(t, DoctorConfig{WorkDir: wd, PipelineFile: dip})
	if c := doctorCheck(t, r, "LLM Providers"); c.Status == CheckStatusOK && strings.Contains(c.Message, "not needed") {
		t.Fatalf("a pipeline with unseen subgraphs must not skip the key check: %+v", c)
	}
}

func TestDoctor_NoPipelineClaudeOnlyWarnsNotFails(t *testing.T) {
	// A Claude Code user with no API key runs bare `tracker doctor`: the
	// starters work, so missing keys are a warning that says so, not a failure.
	wd := doctorEnv(t, fakeClaudeDir(t))
	r := runDoctor(t, DoctorConfig{WorkDir: wd})
	c := doctorCheck(t, r, "LLM Providers")
	if c.Status != CheckStatusWarn || !strings.Contains(c.Message, "Claude Code") {
		t.Fatalf("no keys, claude available, no pipeline: providers check = %+v, want a warning naming Claude Code", c)
	}
	wd = doctorEnv(t)
	if c := doctorCheck(t, runDoctor(t, DoctorConfig{WorkDir: wd}), "LLM Providers"); c.Status != CheckStatusError {
		t.Fatalf("no keys and no claude: providers check = %+v, want error", c)
	}
}

func TestDoctor_StartersHaveNoPipelineWarnings(t *testing.T) {
	wd := doctorEnv(t, fakeClaudeDir(t))
	for _, name := range []string{"explain", "quickfix"} {
		c := doctorCheck(t, runDoctor(t, DoctorConfig{WorkDir: wd, PipelineFile: name}), "Pipeline File")
		for _, d := range c.Details {
			if d.Status != CheckStatusOK {
				t.Errorf("%s: pipeline check detail %+v, want no warnings", name, d)
			}
		}
	}
}

func TestDoctor_ProbesClaudeOnce(t *testing.T) {
	// `claude --version` can take seconds on a cold start; doctor runs it once.
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	script := "#!/bin/sh\necho x >> '" + count + "'\necho '2.1.302 (Claude Code)'\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	wd := doctorEnv(t, dir)
	runDoctor(t, DoctorConfig{WorkDir: wd}, WithDoctorGraphs(doctorGraph("auto"), nil))
	b, _ := os.ReadFile(count)
	if n := strings.Count(string(b), "x"); n != 1 {
		t.Fatalf("claude --version ran %d times, want 1", n)
	}
}

// claudeAt writes a fake claude reporting version into a new dir.
func claudeAt(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\necho '"+version+" (Claude Code)'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDoctor_WarnsWhenSeveralClaudesAreOnPath(t *testing.T) {
	// #719: a stale claude earlier on PATH ran instead of the current one.
	stale, current := claudeAt(t, "2.1.269"), claudeAt(t, "2.1.303")
	wd := doctorEnv(t, stale, current)
	c := doctorCheck(t, runDoctor(t, DoctorConfig{WorkDir: wd}), "Optional Binaries")
	var text string
	for _, d := range c.Details {
		text += d.Message + " " + d.Hint + "\n"
	}
	for _, want := range []string{
		filepath.Join(stale, "claude"), "2.1.269", filepath.Join(current, "claude"), "2.1.303", "TRACKER_CLAUDE_BIN",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("binaries check missing %q:\n%s", want, text)
		}
	}
	if c.Status == CheckStatusOK {
		t.Errorf("several claudes on PATH must be a warning, got %+v", c)
	}
}

func TestDoctor_ShowsTheNamedClaude(t *testing.T) {
	stale, current := claudeAt(t, "2.1.269"), claudeAt(t, "2.1.303")
	wd := doctorEnv(t, stale)
	t.Setenv("TRACKER_CLAUDE_BIN", filepath.Join(current, "claude"))
	c := doctorCheck(t, runDoctor(t, DoctorConfig{WorkDir: wd}), "Optional Binaries")
	var text string
	for _, d := range c.Details {
		text += d.Message + "\n"
	}
	if !strings.Contains(text, "2.1.303") || !strings.Contains(text, filepath.Join(current, "claude")) || !strings.Contains(text, "TRACKER_CLAUDE_BIN") {
		t.Fatalf("binaries check must show the TRACKER_CLAUDE_BIN claude:\n%s", text)
	}
	if strings.Contains(text, "2.1.269") {
		t.Errorf("with TRACKER_CLAUDE_BIN set, the PATH claude is irrelevant:\n%s", text)
	}
}

func TestDoctor_BrokenOverrideIsAnErrorEvenWithClaudeOnPath(t *testing.T) {
	// The run refuses a broken TRACKER_CLAUDE_BIN (no PATH fallback), so
	// doctor must say so — not pass on an unrelated claude found on PATH.
	wd := doctorEnv(t, claudeAt(t, "2.1.303"))
	missing := filepath.Join(t.TempDir(), "missing", "claude")
	t.Setenv("TRACKER_CLAUDE_BIN", missing)
	r := runDoctor(t, DoctorConfig{WorkDir: wd}, WithDoctorGraphs(doctorGraph("claude-code"), nil))
	c := doctorCheck(t, r, "Optional Binaries")
	var text string
	for _, d := range c.Details {
		text += d.Message + "\n"
	}
	if c.Status != CheckStatusError || !strings.Contains(text, missing) || !strings.Contains(text, "TRACKER_CLAUDE_BIN") {
		t.Fatalf("broken override with a claude-code pipeline: binaries check = %+v, want an error naming it", c)
	}
}
