// ABOUTME: Tests for embedded workflow catalog, resolution, and init command.
// ABOUTME: Verifies lookup, listing, parsing, resolution order, and flag parsing.
package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/2389-research/dippin-lang/ir"
	"github.com/2389-research/dippin-lang/parser"
	tracker "github.com/2389-research/tracker"
	"github.com/2389-research/tracker/pipeline"
)

func TestLookupBuiltinWorkflowKnown(t *testing.T) {
	for _, name := range []string{"ask_and_execute", "build_product", "build_product_with_superspec", "deep_review"} {
		info, ok := lookupBuiltinWorkflow(name)
		if !ok {
			t.Errorf("lookupBuiltinWorkflow(%q) returned false", name)
			continue
		}
		if info.Name != name {
			t.Errorf("info.Name = %q, want %q", info.Name, name)
		}
		if info.DisplayName == "" {
			t.Errorf("info.DisplayName is empty for %q", name)
		}
		if info.Goal == "" {
			t.Errorf("info.Goal is empty for %q", name)
		}
		if info.File == "" {
			t.Errorf("info.File is empty for %q", name)
		}
	}
}

func TestLookupBuiltinWorkflowUnknown(t *testing.T) {
	_, ok := lookupBuiltinWorkflow("nonexistent_workflow")
	if ok {
		t.Error("lookupBuiltinWorkflow should return false for unknown workflow")
	}
}

func TestListBuiltinWorkflowsReturnsFour(t *testing.T) {
	workflows := listBuiltinWorkflows()
	if len(workflows) != 4 {
		t.Errorf("listBuiltinWorkflows returned %d workflows, want 4", len(workflows))
	}
	// Verify sorted order.
	for i := 1; i < len(workflows); i++ {
		if workflows[i-1].Name >= workflows[i].Name {
			t.Errorf("workflows not sorted: %q >= %q", workflows[i-1].Name, workflows[i].Name)
		}
	}
}

func TestEmbeddedWorkflowsParse(t *testing.T) {
	for _, wf := range listBuiltinWorkflows() {
		graph, err := loadEmbeddedPipeline(wf)
		if err != nil {
			t.Errorf("loadEmbeddedPipeline(%q) error: %v", wf.Name, err)
			continue
		}
		if graph.StartNode == "" {
			t.Errorf("workflow %q has no start node", wf.Name)
		}
		if graph.ExitNode == "" {
			t.Errorf("workflow %q has no exit node", wf.Name)
		}
		if len(graph.Nodes) == 0 {
			t.Errorf("workflow %q has no nodes", wf.Name)
		}
	}
}

func TestResolvePipelineSourceFilesystemPath(t *testing.T) {
	// Paths with / or .dip extension are treated as filesystem paths.
	path, embedded, _, err := resolvePipelineSource("examples/build_product.dip")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if embedded {
		t.Error("expected embedded=false for filesystem path")
	}
	if path != "examples/build_product.dip" {
		t.Errorf("path = %q, want %q", path, "examples/build_product.dip")
	}
}

func TestResolvePipelineSourceLocalFileWins(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create a local .dip file that shadows the built-in.
	if err := os.WriteFile("build_product.dip", []byte("workflow Local\n  goal: \"test\"\n  start: S\n  exit: E\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	path, embedded, _, err := resolvePipelineSource("build_product")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if embedded {
		t.Error("expected local file to win over embedded")
	}
	if path != "build_product.dip" {
		t.Errorf("path = %q, want %q", path, "build_product.dip")
	}
}

func TestResolvePipelineSourceFallsToEmbedded(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	// No local file — should resolve to embedded.
	_, embedded, info, err := resolvePipelineSource("build_product")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !embedded {
		t.Error("expected embedded=true when no local file exists")
	}
	if info.Name != "build_product" {
		t.Errorf("info.Name = %q, want %q", info.Name, "build_product")
	}
}

func TestResolvePipelineSourceUnknownErrors(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	_, _, _, err := resolvePipelineSource("totally_unknown_workflow")
	if err == nil {
		t.Fatal("expected error for unknown workflow")
	}
	if !strings.Contains(err.Error(), "unknown pipeline") {
		t.Errorf("expected 'unknown pipeline' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "build_product") {
		t.Errorf("expected available workflows listed in error, got: %v", err)
	}
}

func TestParseFlagsWorkflows(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "workflows"})
	if err != nil {
		t.Fatalf("parseFlags error: %v", err)
	}
	if cfg.mode != modeWorkflows {
		t.Errorf("mode = %q, want %q", cfg.mode, modeWorkflows)
	}
}

func TestParseFlagsInit(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "init", "build_product"})
	if err != nil {
		t.Fatalf("parseFlags error: %v", err)
	}
	if cfg.mode != modeInit {
		t.Errorf("mode = %q, want %q", cfg.mode, modeInit)
	}
	if cfg.pipelineFile != "build_product" {
		t.Errorf("pipelineFile = %q, want %q", cfg.pipelineFile, "build_product")
	}
}

func TestParseFlagsInitNoArg(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "init"})
	if err != nil {
		t.Fatalf("parseFlags error: %v", err)
	}
	if cfg.mode != modeInit {
		t.Errorf("mode = %q, want %q", cfg.mode, modeInit)
	}
	if cfg.pipelineFile != "" {
		t.Errorf("pipelineFile = %q, want empty", cfg.pipelineFile)
	}
}

func TestExecuteInitCreatesFile(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	err := executeInit(runConfig{pipelineFile: "build_product"})
	if err != nil {
		t.Fatalf("executeInit error: %v", err)
	}

	outPath := filepath.Join(dir, "build_product.dip")
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("expected build_product.dip to exist: %v", err)
	}

	content, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.HasPrefix(string(content), "workflow BuildProduct") {
		t.Errorf("expected file to start with 'workflow BuildProduct', got: %.50s...", string(content))
	}
}

func TestExecuteInitRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create the file first.
	if err := os.WriteFile("build_product.dip", []byte("existing"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	err := executeInit(runConfig{pipelineFile: "build_product"})
	if err == nil {
		t.Fatal("expected error when file already exists")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected 'already exists' error, got: %v", err)
	}
}

func TestExecuteInitUnknownWorkflow(t *testing.T) {
	err := executeInit(runConfig{pipelineFile: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown workflow")
	}
	if !strings.Contains(err.Error(), "unknown workflow") {
		t.Errorf("expected 'unknown workflow' error, got: %v", err)
	}
}

func TestWorkflowDisplayNames(t *testing.T) {
	expected := map[string]string{
		"ask_and_execute":              "AskAndExecute",
		"build_product":                "BuildProduct",
		"build_product_with_superspec": "BuildProductWithSuperspec",
		"deep_review":                  "DeepReview",
	}
	for name, wantDisplay := range expected {
		info, ok := lookupBuiltinWorkflow(name)
		if !ok {
			t.Errorf("workflow %q not found", name)
			continue
		}
		if info.DisplayName != wantDisplay {
			t.Errorf("workflow %q DisplayName = %q, want %q", name, info.DisplayName, wantDisplay)
		}
	}
}

// directiveNodes returns, for a .dip source, the node IDs that declare a
// prompt_file / system_prompt_file / command_file directive, keyed by the
// graph attr the resolved body lands in.
func directiveNodes(t *testing.T, source, filename string) map[string][]string {
	t.Helper()
	w, err := parser.NewParser(source, filename).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out := map[string][]string{}
	for _, n := range w.Nodes {
		switch c := n.Config.(type) {
		case ir.AgentConfig:
			if c.PromptFile != "" {
				out["prompt"] = append(out["prompt"], n.ID)
			}
			if c.SystemPromptFile != "" {
				out["system_prompt"] = append(out["system_prompt"], n.ID)
			}
		case ir.ToolConfig:
			if c.CommandFile != "" {
				out["tool_command"] = append(out["tool_command"], n.ID)
			}
		}
	}
	return out
}

// TestEmbeddedBuildProductResolvesSidecars guards the embedded load path: a
// built-in's *_file directives must resolve from the embed FS (no file on
// disk is consulted), so every directive-bearing node ends up with a body.
func TestEmbeddedBuildProductResolvesSidecars(t *testing.T) {
	dir := t.TempDir() // an empty cwd: nothing can be resolved from disk
	origDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	info, ok := lookupBuiltinWorkflow("build_product")
	if !ok {
		t.Fatal("build_product not embedded")
	}
	graph, err := loadEmbeddedPipeline(info)
	if err != nil {
		t.Fatalf("loadEmbeddedPipeline: %v", err)
	}
	prompt := graph.Nodes["SpecLint"].Attrs["prompt"]
	if !strings.Contains(prompt, "SPEC COHERENCE PREFLIGHT") {
		t.Errorf("SpecLint prompt not resolved from embed FS: %.80q", prompt)
	}

	src, _, err := tracker.OpenWorkflow(info.Name)
	if err != nil {
		t.Fatal(err)
	}
	for attr, ids := range directiveNodes(t, string(src), info.File) {
		for _, id := range ids {
			n := graph.Nodes[id]
			if n == nil {
				t.Errorf("node %q missing from graph", id)
				continue
			}
			if n.Attrs[attr] == "" {
				t.Errorf("node %q has a *_file directive but empty %q after embedded load", id, attr)
			}
		}
	}
}

// TestEmbeddedSimulateAndValidateFromEmptyDir drives the two CLI read paths
// that go through the library's source-string entry points (simulate →
// ValidateSource; validate → loadEmbeddedPipeline) with a bare built-in name
// from a cwd that has no sidecars.
func TestEmbeddedSimulateAndValidateFromEmptyDir(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	var out bytes.Buffer
	if err := runSimulateCmd("build_product", "", &out); err != nil {
		t.Fatalf("simulate build_product: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "SpecLint") {
		t.Errorf("simulate output missing SpecLint:\n%s", out.String())
	}
	out.Reset()
	if err := runValidateCmd("build_product", "", &out); err != nil {
		t.Fatalf("validate build_product: %v\n%s", err, out.String())
	}
}

// TestExecuteInitCopiesSidecarsAndLoadsFromDisk: for EVERY built-in,
// `tracker init <name>` must write the .dip plus every sidecar its *_file
// directives reference (derived from the IR, not a naming convention — the
// superspec variant shares build_product's SpecLint.md), and the copied tree
// must load through the ordinary disk path with the same resolved bodies as
// the embedded copy, from a cwd that is NOT the init dir.
func TestExecuteInitCopiesSidecarsAndLoadsFromDisk(t *testing.T) {
	for _, wf := range listBuiltinWorkflows() {
		t.Run(wf.Name, func(t *testing.T) {
			dir := t.TempDir()
			origDir, _ := os.Getwd()
			if err := os.Chdir(dir); err != nil {
				t.Fatalf("chdir: %v", err)
			}
			t.Cleanup(func() { os.Chdir(origDir) })

			if err := executeInit(runConfig{pipelineFile: wf.Name}); err != nil {
				t.Fatalf("executeInit: %v", err)
			}
			sidecars, err := workflowSidecars(wf)
			if err != nil {
				t.Fatal(err)
			}
			src, _, err := tracker.OpenWorkflow(wf.Name)
			if err != nil {
				t.Fatal(err)
			}
			if want := len(directiveNodes(t, string(src), wf.File)); want > 0 && len(sidecars) == 0 {
				t.Fatalf("%s declares directives but init found no sidecars", wf.Name)
			}
			for _, sc := range sidecars {
				if _, err := os.Stat(filepath.FromSlash(sc.dest)); err != nil {
					t.Errorf("sidecar %s not created: %v", sc.dest, err)
				}
			}

			embedded, err := loadEmbeddedPipeline(wf)
			if err != nil {
				t.Fatalf("embedded load: %v", err)
			}
			// Load the copy from a different cwd: directives must anchor at the
			// file, not the process cwd.
			if err := os.Chdir(t.TempDir()); err != nil {
				t.Fatal(err)
			}
			disk, err := loadPipeline(filepath.Join(dir, wf.Name+".dip"), "")
			if err != nil {
				t.Fatalf("disk load of init copy: %v", err)
			}
			// The local copy also shadows the built-in for the CLI: validate must
			// work from the init dir itself.
			if err := os.Chdir(dir); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := runValidateCmd(wf.Name, "", &out); err != nil {
				t.Fatalf("validate %s in init dir: %v\n%s", wf.Name, err, out.String())
			}
			assertSameBodies(t, embedded, disk)
		})
	}
}

// assertSameBodies compares every node's prompt / system_prompt / tool_command
// between two graphs.
func assertSameBodies(t *testing.T, want, got *pipeline.Graph) {
	t.Helper()
	for id, en := range want.Nodes {
		dn := got.Nodes[id]
		if dn == nil {
			t.Errorf("node %q missing", id)
			continue
		}
		for _, attr := range []string{"prompt", "system_prompt", "tool_command"} {
			if en.Attrs[attr] != dn.Attrs[attr] {
				t.Errorf("node %q %s differs", id, attr)
			}
		}
	}
}

// TestEmbeddedSidecarsFollowDirectives pins the sidecar set to what the engine
// materializes (pipeline.WorkflowFiles): superspec has no prompts/scripts dirs
// of its own, so it gets build_product's SpecLint.md and nothing else;
// build_product gets its whole prompts/ + scripts/ tree — including the
// sourced scripts/build_product/lib/ helpers no directive names — minus the
// .dip itself.
func TestEmbeddedSidecarsFollowDirectives(t *testing.T) {
	info, _ := lookupBuiltinWorkflow("build_product_with_superspec")
	sidecars, err := workflowSidecars(info)
	if err != nil {
		t.Fatal(err)
	}
	if len(sidecars) != 1 || sidecars[0].dest != "prompts/build_product/SpecLint.md" || sidecars[0].embedPath != "examples/prompts/build_product/SpecLint.md" {
		t.Fatalf("superspec sidecars = %+v", sidecars)
	}
	bp, _ := lookupBuiltinWorkflow("build_product")
	bpSidecars, err := workflowSidecars(bp)
	if err != nil {
		t.Fatal(err)
	}
	want, err := pipeline.WorkflowFiles(tracker.EmbeddedWorkflowFS(), "examples", "build_product")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, sc := range bpSidecars {
		got = append(got, sc.dest)
		if sc.embedPath != "examples/"+sc.dest {
			t.Errorf("sidecar %s embedPath = %s", sc.dest, sc.embedPath)
		}
	}
	var wantDests []string
	for _, p := range want {
		if p != "build_product.dip" {
			wantDests = append(wantDests, p)
		}
	}
	if strings.Join(got, "\n") != strings.Join(wantDests, "\n") {
		t.Errorf("init sidecars drifted from the materialized set:\n got %v\nwant %v", got, wantDests)
	}
	for _, must := range []string{"scripts/build_product/lib/verify.sh", "scripts/build_product/lib/gitignore.sh", "scripts/build_product/Setup.sh", "prompts/build_product/SpecLint.md"} {
		if !slices.Contains(got, must) {
			t.Errorf("build_product init set lacks %s", must)
		}
	}
	// 15 prompts + 18 scripts (#640 A4 added CheckVerifyFailBudget.sh) + 7 lib
	// files; the shell fixture suites (*_test.sh, test_helpers.sh) beside them
	// are never part of the set.
	if len(got) != 40 {
		t.Errorf("build_product sidecars = %d, want 40: %v", len(got), got)
	}
	for _, p := range got {
		if strings.HasSuffix(p, "_test.sh") || strings.HasSuffix(p, "/test_helpers.sh") {
			t.Errorf("init set ships test fixture %s", p)
		}
	}
}

// TestExecuteInitCopyCanSourceLibViaWorkflowDir: the init copy of
// build_product is a disk load, so ${graph.workflow_dir} is the init dir
// itself. Its Setup.sh must find scripts/build_product/lib/ there and install
// the runtime files byte-for-byte — the same thing the embedded run gets from
// the materialized copy.
func TestExecuteInitCopyCanSourceLibViaWorkflowDir(t *testing.T) {
	shPath, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not on PATH")
	}
	initDir := t.TempDir()
	t.Chdir(initDir)
	if err := executeInit(runConfig{pipelineFile: "build_product"}); err != nil {
		t.Fatalf("executeInit: %v", err)
	}
	for _, p := range []string{"scripts/build_product/lib/verify.sh", "scripts/build_product/lib/ci-probe.sh", "scripts/build_product/lib/gitignore.sh"} {
		if _, err := os.Stat(filepath.FromSlash(p)); err != nil {
			t.Fatalf("init copy lacks %s: %v", p, err)
		}
	}
	for _, p := range []string{"scripts/build_product/Setup_test.sh", "scripts/build_product/test_helpers.sh", "scripts/build_product/lib/verify_test.sh"} {
		if _, err := os.Stat(filepath.FromSlash(p)); err == nil {
			t.Errorf("init copy ships test fixture %s", p)
		}
	}
	graph, err := loadPipeline(filepath.Join(initDir, "build_product.dip"), "")
	if err != nil {
		t.Fatalf("disk load of init copy: %v", err)
	}
	if got := graph.Attrs[pipeline.WorkflowDirAttr]; got != initDir {
		t.Fatalf("workflow_dir = %q, want init dir %q", got, initDir)
	}
	// Expand the Setup body exactly as the engine's tool handler would
	// (${graph.workflow_dir} is graph-attr sourced, allowlisted in
	// tool-command mode) and run it in a fresh workdir.
	body := graph.Nodes["Setup"].Attrs["tool_command"]
	expanded, err := pipeline.ExpandVariables(body, pipeline.NewPipelineContext(), nil, graph.Attrs, false, true)
	if err != nil {
		t.Fatalf("expand Setup body: %v", err)
	}
	if strings.Contains(expanded, "${graph.workflow_dir}") {
		t.Fatal("workflow_dir left unexpanded in Setup body")
	}
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "SPEC.md"), []byte("spec\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(shPath, "-c", expanded)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Setup.sh from init copy failed: %v\n%s", err, out)
	}
	if !strings.HasSuffix(string(out), "setup-ready") {
		t.Fatalf("Setup.sh did not end with the setup-ready marker:\n%s", out)
	}
	for _, f := range []string{"verify.sh", "ci-probe.sh", "iface-reachability-rubric.md"} {
		want, _ := os.ReadFile(filepath.Join(initDir, "scripts", "build_product", "lib", f))
		got, err := os.ReadFile(filepath.Join(workDir, ".ai", "build", f))
		if err != nil {
			t.Fatalf("Setup did not install .ai/build/%s: %v", f, err)
		}
		if string(got) != string(want) {
			t.Errorf(".ai/build/%s differs from the lib/ sidecar", f)
		}
	}
}

// TestExecuteInitRefusesOverwriteSidecar: an existing sidecar file blocks init
// before anything is written, the same way an existing .dip does.
func TestExecuteInitRefusesOverwriteSidecar(t *testing.T) {
	info, _ := lookupBuiltinWorkflow("build_product")
	sidecars, err := workflowSidecars(info)
	if err != nil {
		t.Fatal(err)
	}
	if len(sidecars) == 0 {
		t.Fatal("build_product ships no sidecars")
	}
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	existing := filepath.FromSlash(sidecars[0].dest)
	if err := os.MkdirAll(filepath.Dir(existing), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = executeInit(runConfig{pipelineFile: "build_product"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected already-exists refusal, got %v", err)
	}
	if _, statErr := os.Stat("build_product.dip"); statErr == nil {
		t.Error("build_product.dip was written despite the refusal")
	}
	if got, _ := os.ReadFile(existing); string(got) != "mine" {
		t.Errorf("existing sidecar was overwritten: %q", got)
	}
}
