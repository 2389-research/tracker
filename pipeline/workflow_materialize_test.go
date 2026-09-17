// ABOUTME: Tests for MaterializeWorkflow / MaterializeBuiltinWorkflowDir — the embedded
// ABOUTME: built-in sidecar tree copied into <workDir>/.tracker/workflow/<name>/ so
// ABOUTME: ${graph.workflow_dir} resolves for a workflow that has no on-disk directory.
package pipeline

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
)

// fixtureWorkflowFS mirrors the embed layout: examples/<name>.dip plus
// examples/prompts/<name>/ and examples/scripts/<name>/ subtrees. The .dip
// references one prompt sidecar by directive; lib/hello.txt is deliberately
// NOT referenced by any directive — it must be materialized anyway.
func fixtureWorkflowFS() fstest.MapFS {
	dip := `workflow X
  goal: "materialize test"
  start: Step
  exit: Done

  tool Step
    command_file: scripts/x/step.sh

  agent Done
    label: Done

  edges
    Step -> Done
`
	return fstest.MapFS{
		"examples/x.dip":                          {Data: []byte(dip)},
		"examples/scripts/x/step.sh":              {Data: []byte("echo step\n")},
		"examples/scripts/x/lib/hello.txt":        {Data: []byte("hello from lib\n")},
		"examples/prompts/x/Shared.md":            {Data: []byte("shared prompt\n")},
		"examples/prompts/other/Borrowed.md":      {Data: []byte("borrowed\n")},
		"examples/scripts/unrelated/notcopied.sh": {Data: []byte("nope\n")},
		"examples/scripts/x/step_test.sh":         {Data: []byte("fixture\n")},
		"examples/scripts/x/test_helpers.sh":      {Data: []byte("fixture\n")},
		"examples/scripts/x/lib/hello_test.sh":    {Data: []byte("fixture\n")},
		"examples/other.dip":                      {Data: []byte("workflow Other\n")},
	}
}

func TestMaterializeWorkflow_CopiesTreeAndReturnsAbsoluteDir(t *testing.T) {
	workDir := t.TempDir()
	dir, err := MaterializeWorkflow(fixtureWorkflowFS(), "examples", "x", workDir)
	if err != nil {
		t.Fatalf("MaterializeWorkflow: %v", err)
	}
	if !filepath.IsAbs(dir) {
		t.Errorf("returned dir %q is not absolute", dir)
	}
	want := filepath.Join(workDir, ".tracker", "workflow", "x")
	if dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}
	for rel, content := range map[string]string{
		"x.dip":                   "workflow X",
		"scripts/x/step.sh":       "echo step\n",
		"scripts/x/lib/hello.txt": "hello from lib\n",
		"prompts/x/Shared.md":     "shared prompt\n",
	} {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("%s not materialized: %v", rel, err)
			continue
		}
		if !strings.Contains(string(data), content) {
			t.Errorf("%s content = %q, want to contain %q", rel, data, content)
		}
	}
	for _, rel := range []string{"prompts/other/Borrowed.md", "scripts/unrelated/notcopied.sh", "other.dip"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err == nil {
			t.Errorf("%s belongs to another workflow and must not be materialized", rel)
		}
	}
	// Shell fixture suites beside the scripts never ship into a user's project.
	for _, rel := range []string{"scripts/x/step_test.sh", "scripts/x/test_helpers.sh", "scripts/x/lib/hello_test.sh"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err == nil {
			t.Errorf("%s is a test fixture and must not be materialized", rel)
		}
	}
}

func TestMaterializeWorkflow_CopiesDirectiveSidecarsOutsideOwnDirs(t *testing.T) {
	// A built-in may reference a sidecar in ANOTHER workflow's directory
	// (superspec's SpecLint loads prompts/build_product/SpecLint.md). The
	// directive-referenced file is copied even though it is not under
	// prompts/<name>/ or scripts/<name>/.
	fsys := fixtureWorkflowFS()
	fsys["examples/x.dip"] = &fstest.MapFile{Data: []byte(`workflow X
  goal: "shared sidecar"
  start: Ask
  exit: Done

  agent Ask
    prompt_file: prompts/other/Borrowed.md

  agent Done
    label: Done

  edges
    Ask -> Done
`)}
	workDir := t.TempDir()
	dir, err := MaterializeWorkflow(fsys, "examples", "x", workDir)
	if err != nil {
		t.Fatalf("MaterializeWorkflow: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "prompts", "other", "Borrowed.md")); err != nil {
		t.Errorf("directive-referenced sidecar outside prompts/x not materialized: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "scripts", "unrelated", "notcopied.sh")); err == nil {
		t.Error("unreferenced file from another workflow must not be materialized")
	}
}

func TestMaterializeWorkflow_FileModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	workDir := t.TempDir()
	dir, err := MaterializeWorkflow(fixtureWorkflowFS(), "examples", "x", workDir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, "scripts", "x", "step.sh"))
	if err != nil {
		t.Fatal(err)
	}
	// Scripts are sourced / `sh`'d by tool commands, never exec'd directly, so
	// the materialized copy is deliberately not executable.
	if got := st.Mode().Perm(); got != 0o644 {
		t.Errorf("file mode = %o, want 0644", got)
	}
	dst, err := os.Stat(filepath.Join(dir, "scripts", "x"))
	if err != nil {
		t.Fatal(err)
	}
	if got := dst.Mode().Perm(); got != 0o755 {
		t.Errorf("dir mode = %o, want 0755", got)
	}
}

func TestMaterializeWorkflow_SecondCallReplacesStaleContent(t *testing.T) {
	workDir := t.TempDir()
	fsys := fixtureWorkflowFS()
	dir, err := MaterializeWorkflow(fsys, "examples", "x", workDir)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a stale materialization: an edited file and a leftover that no
	// longer exists in the source tree (e.g. from an older binary).
	hello := filepath.Join(dir, "scripts", "x", "lib", "hello.txt")
	if err := os.WriteFile(hello, []byte("STALE"), 0o644); err != nil {
		t.Fatal(err)
	}
	leftover := filepath.Join(dir, "scripts", "x", "removed.sh")
	if err := os.WriteFile(leftover, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := MaterializeWorkflow(fsys, "examples", "x", workDir); err != nil {
		t.Fatalf("second MaterializeWorkflow: %v", err)
	}
	data, err := os.ReadFile(hello)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello from lib\n" {
		t.Errorf("stale content survived re-materialization: %q", data)
	}
	if _, err := os.Stat(leftover); err == nil {
		t.Error("leftover file from a previous materialization survived — tree must be replaced, not merged")
	}
	// No temp staging dir is left behind.
	entries, _ := os.ReadDir(filepath.Join(workDir, ".tracker", "workflow"))
	for _, e := range entries {
		if e.Name() != "x" {
			t.Errorf("unexpected entry %q left in .tracker/workflow", e.Name())
		}
	}
}

func TestMaterializeWorkflow_RefusesSymlinkedDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	for _, rel := range []string{".tracker", ".tracker/workflow", ".tracker/workflow/x"} {
		t.Run(rel, func(t *testing.T) {
			workDir := t.TempDir()
			outside := t.TempDir()
			link := filepath.Join(workDir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, link); err != nil {
				t.Fatal(err)
			}
			_, err := MaterializeWorkflow(fixtureWorkflowFS(), "examples", "x", workDir)
			if err == nil || !strings.Contains(err.Error(), "symlink") {
				t.Fatalf("expected symlink refusal for %s, got %v", rel, err)
			}
			if entries, _ := os.ReadDir(outside); len(entries) != 0 {
				t.Errorf("wrote through symlink %s into %s: %v", rel, outside, entries)
			}
		})
	}
}

func TestMaterializeWorkflow_RejectsBadInputs(t *testing.T) {
	workDir := t.TempDir()
	fsys := fixtureWorkflowFS()
	for _, name := range []string{"", "..", "a/b", `a\b`, "nope"} {
		if _, err := MaterializeWorkflow(fsys, "examples", name, workDir); err == nil {
			t.Errorf("name %q: expected error", name)
		}
	}
	// A directive that escapes the root is refused rather than copied.
	fsys["examples/x.dip"] = &fstest.MapFile{Data: []byte(`workflow X
  goal: "escape"
  start: Step
  exit: Done

  tool Step
    command_file: ../../etc/passwd

  agent Done
    label: Done

  edges
    Step -> Done
`)}
	if _, err := MaterializeWorkflow(fsys, "examples", "x", workDir); err == nil {
		t.Error("expected error for a directive path that escapes the workflow root")
	}
}

func TestMaterializeBuiltinWorkflowDir_SetsAttrAndHonorsAuthorDeclared(t *testing.T) {
	workDir := t.TempDir()
	g := NewGraph("t")
	g.Attrs[WorkflowBuiltinAttr] = "x"
	if err := MaterializeBuiltinWorkflowDir(g, fixtureWorkflowFS(), "examples", workDir); err != nil {
		t.Fatalf("MaterializeBuiltinWorkflowDir: %v", err)
	}
	want := filepath.Join(workDir, ".tracker", "workflow", "x")
	if got := g.Attrs[WorkflowDirAttr]; got != want {
		t.Errorf("workflow_dir = %q, want %q", got, want)
	}

	// Author-declared (including explicit empty) wins: nothing materialized.
	for _, declared := range []string{"/author/declared", ""} {
		workDir := t.TempDir()
		g := NewGraph("t")
		g.Attrs[WorkflowBuiltinAttr] = "x"
		g.Attrs[WorkflowDirAttr] = declared
		if err := MaterializeBuiltinWorkflowDir(g, fixtureWorkflowFS(), "examples", workDir); err != nil {
			t.Fatal(err)
		}
		if got := g.Attrs[WorkflowDirAttr]; got != declared {
			t.Errorf("author-declared workflow_dir %q clobbered: %q", declared, got)
		}
		if _, err := os.Stat(filepath.Join(workDir, ".tracker")); err == nil {
			t.Errorf("declared workflow_dir %q: must not materialize", declared)
		}
	}

	// No workflow_builtin attr: a no-op.
	workDir2 := t.TempDir()
	g2 := NewGraph("t")
	if err := MaterializeBuiltinWorkflowDir(g2, fixtureWorkflowFS(), "examples", workDir2); err != nil {
		t.Fatal(err)
	}
	if _, ok := g2.Attrs[WorkflowDirAttr]; ok {
		t.Error("non-builtin graph must not get workflow_dir")
	}
}

func TestSeedWorkflowDir(t *testing.T) {
	g := NewGraph("t")
	SeedWorkflowDir(g, filepath.Join("rel", "fixture.dip"))
	got := g.Attrs[WorkflowDirAttr]
	if !filepath.IsAbs(got) || filepath.Base(got) != "rel" {
		t.Errorf("SeedWorkflowDir = %q, want absolute dir ending in rel", got)
	}
	for _, declared := range []string{"/author/declared", ""} {
		g := NewGraph("t")
		g.Attrs[WorkflowDirAttr] = declared
		SeedWorkflowDir(g, "/elsewhere/fixture.dip")
		if g.Attrs[WorkflowDirAttr] != declared {
			t.Errorf("declared %q clobbered: %q", declared, g.Attrs[WorkflowDirAttr])
		}
	}
}
