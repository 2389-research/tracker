// ABOUTME: Tests that ${graph.workflow_dir} resolves for embedded built-ins on the library
// ABOUTME: path (materialized into <workDir>/.tracker/workflow/<name>/) and for Path refs.
package tracker

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// countEmbeddedSidecars returns how many files live under examples/prompts/<name>
// and examples/scripts/<name> in the embed FS.
// countEmbeddedSidecars counts the prompts/<name> + scripts/<name> files in
// the embed FS that a run materializes — every file except the shell fixture
// suites (`*_test.sh`, `test_helpers.sh`), which never ship to a workdir.
func countEmbeddedSidecars(t *testing.T, name string) int {
	t.Helper()
	n := 0
	for _, sub := range []string{"examples/prompts/" + name, "examples/scripts/" + name} {
		_ = fs.WalkDir(embeddedWorkflows, sub, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && !isTestFixture(p) {
				n++
			}
			return nil
		})
	}
	return n
}

func isTestFixture(p string) bool {
	base := filepath.Base(p)
	return base == "test_helpers.sh" || strings.HasSuffix(base, "_test.sh")
}

func TestNewEngine_BuiltinMaterializesWorkflowDir(t *testing.T) {
	chdirEmpty(t)
	workDir := t.TempDir()
	src, info, err := OpenWorkflow("build_product")
	if err != nil {
		t.Fatal(err)
	}
	eng, err := NewEngine(string(src), Config{
		WorkingDir: workDir,
		Source:     info.Ref(),
		LLMClient:  &stubCompleter{response: &llm.Response{Message: llm.AssistantMessage("done"), FinishReason: llm.FinishReason{Reason: "stop"}}},
		Git:        &GitConfig{Preflight: GitPreflightOff},
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()

	want := filepath.Join(workDir, ".tracker", "workflow", "build_product")
	got := eng.inner.Graph().Attrs[pipeline.WorkflowDirAttr]
	if got != want {
		t.Fatalf("workflow_dir = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(want, "build_product.dip")); err != nil {
		t.Errorf("build_product.dip not materialized: %v", err)
	}
	// Every sidecar in the embed FS is present, not just directive-referenced ones.
	wantSidecars := countEmbeddedSidecars(t, "build_product")
	if wantSidecars < 30 {
		t.Fatalf("embed FS has only %d build_product sidecars — fixture drifted?", wantSidecars)
	}
	gotSidecars := 0
	for _, sub := range []string{"prompts", "scripts"} {
		_ = filepath.WalkDir(filepath.Join(want, sub, "build_product"), func(_ string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				gotSidecars++
			}
			return nil
		})
	}
	if gotSidecars != wantSidecars {
		t.Errorf("materialized %d sidecars, embed FS has %d", gotSidecars, wantSidecars)
	}
	// The fixture suites beside the scripts are NOT materialized.
	for _, rel := range []string{"scripts/build_product/Setup_test.sh", "scripts/build_product/test_helpers.sh", "scripts/build_product/lib/verify_test.sh"} {
		if _, err := os.Stat(filepath.Join(want, filepath.FromSlash(rel))); err == nil {
			t.Errorf("test fixture %s was materialized into the workdir", rel)
		}
	}
	// Nothing was materialized into cwd — only into the configured workdir.
	if _, err := os.Stat(".tracker"); err == nil {
		t.Error("materialized into cwd instead of WorkingDir")
	}
}

// Read-only entry points on a built-in must not touch disk.
func TestReadOnlyEntryPoints_DoNotMaterialize(t *testing.T) {
	chdirEmpty(t)
	src, info, err := OpenWorkflow("build_product")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Simulate(context.Background(), string(src), WithSource(info.Ref())); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateSource(string(src), WithValidateFormat(FormatDip), WithValidateSource(info.Ref())); err != nil {
		t.Fatal(err)
	}
	if _, err := DescribeInputs(string(src), FormatDip, WithSource(info.Ref())); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(".tracker"); err == nil {
		t.Error("a read-only entry point materialized .tracker/ in cwd")
	}
}

// End-to-end: a tool node in an embedded-style workflow reads a sidecar that
// NO directive references (a sourced lib/ helper) via ${graph.workflow_dir}.
func TestRun_BuiltinToolReadsNonDirectiveSidecar(t *testing.T) {
	const dip = `workflow X
  start: s
  exit: e

  agent s
    label: Start

  tool Read
    command: cat "${graph.workflow_dir}/scripts/x/lib/hello.txt"

  agent e
    label: Exit

  edges
    s -> Read
    Read -> e
`
	synthetic := fstest.MapFS{
		"examples/x.dip":                   {Data: []byte(dip)},
		"examples/scripts/x/lib/hello.txt": {Data: []byte("hello from lib")},
	}
	orig := builtinWorkflowSource
	builtinWorkflowSource = func(name string) (fs.FS, string, error) {
		if name != "x" {
			return orig(name)
		}
		return synthetic, "examples", nil
	}
	t.Cleanup(func() { builtinWorkflowSource = orig })

	graph, _, err := pipeline.LoadDippinWorkflowFS(dip, "examples/x.dip", synthetic)
	if err != nil {
		t.Fatal(err)
	}
	markBuiltinWorkflow(graph, WorkflowInfo{Name: "x"})

	workDir := t.TempDir()
	eng, err := NewEngineFromGraph(context.Background(), graph, Config{
		WorkingDir: workDir,
		LLMClient:  &stubCompleter{response: &llm.Response{Message: llm.AssistantMessage("done"), FinishReason: llm.FinishReason{Reason: "stop"}}},
		Git:        &GitConfig{Preflight: GitPreflightOff},
	})
	if err != nil {
		t.Fatalf("NewEngineFromGraph: %v", err)
	}
	defer eng.Close()
	res, err := eng.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != "success" {
		t.Fatalf("status = %q, context = %v", res.Status, res.Context)
	}
	if got := strings.TrimSpace(res.Context["tool_stdout"]); got != "hello from lib" {
		t.Errorf("tool_stdout = %q, want the non-directive sidecar's content", got)
	}
	// The caller's graph is untouched (the engine runs on a prepared clone).
	if _, ok := graph.Attrs[pipeline.WorkflowDirAttr]; ok {
		t.Error("caller's graph must not be mutated by engine construction")
	}
}

// Library parity with the CLI (#332): a SourceRef{Path} load seeds
// workflow_dir to the file's directory, so a `tracker init` copy run through
// the library resolves ${graph.workflow_dir} the same way `tracker run` does.
func TestLoadDIPSource_PathSeedsWorkflowDir(t *testing.T) {
	dir := t.TempDir()
	dip := initCopy(t, dir)
	chdirEmpty(t)
	src, info, err := ResolveSource("build_product", dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Path != dip {
		t.Fatalf("ResolveSource path = %q, want %q", info.Path, dip)
	}
	graph, _, err := loadDIPSource(src, info.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if got := graph.Attrs[pipeline.WorkflowDirAttr]; got != dir {
		t.Errorf("workflow_dir = %q, want %q", got, dir)
	}
	if _, ok := graph.Attrs[pipeline.WorkflowBuiltinAttr]; ok {
		t.Error("a disk load must not be marked as a built-in")
	}
	// Engine construction on a Path ref keeps the seeded dir (no materialization).
	eng, err := NewEngine(src, Config{
		WorkingDir: dir,
		Source:     info.Ref(),
		LLMClient:  &stubCompleter{response: &llm.Response{Message: llm.AssistantMessage("done"), FinishReason: llm.FinishReason{Reason: "stop"}}},
		Git:        &GitConfig{Preflight: GitPreflightOff},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	if got := eng.inner.Graph().Attrs[pipeline.WorkflowDirAttr]; got != dir {
		t.Errorf("engine workflow_dir = %q, want %q", got, dir)
	}
	if _, err := os.Stat(filepath.Join(dir, ".tracker", "workflow")); err == nil {
		t.Error("a disk load must not materialize a workflow copy")
	}
}
