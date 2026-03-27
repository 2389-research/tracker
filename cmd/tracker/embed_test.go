// ABOUTME: Tests for embedded workflow catalog, resolution, and init command.
// ABOUTME: Verifies lookup, listing, parsing, resolution order, and flag parsing.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLookupBuiltinWorkflowKnown(t *testing.T) {
	for _, name := range []string{"ask_and_execute", "build_product", "build_product_with_superspec"} {
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

func TestListBuiltinWorkflowsReturnsThree(t *testing.T) {
	workflows := listBuiltinWorkflows()
	if len(workflows) != 3 {
		t.Errorf("listBuiltinWorkflows returned %d workflows, want 3", len(workflows))
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
