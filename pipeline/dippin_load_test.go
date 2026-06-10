// ABOUTME: Tests for LoadDippinWorkflow and its FromIR tail split.
// ABOUTME: Verifies source and IR entry points produce byte-equivalent graphs.
package pipeline

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/2389-research/dippin-lang/parser"
)

func TestLoadDippinWorkflowFromIR_ProducesSameGraphAsLoadDippinWorkflow(t *testing.T) {
	source := `workflow test_split
  start: a
  exit: b

  agent a
    label: "Start"

  agent b
    label: "Exit"

  edges
    a -> b
`
	const filename = "test_split.dip"

	graphViaSource, _, err := LoadDippinWorkflow(source, filename)
	if err != nil {
		t.Fatalf("LoadDippinWorkflow: %v", err)
	}

	workflow, err := parser.NewParser(source, filename).Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	graphViaIR, _, err := LoadDippinWorkflowFromIR(workflow, filename)
	if err != nil {
		t.Fatalf("LoadDippinWorkflowFromIR: %v", err)
	}

	if !reflect.DeepEqual(graphViaSource, graphViaIR) {
		t.Errorf("graph divergence between source path and IR path:\n  source: %+v\n  ir:     %+v", graphViaSource, graphViaIR)
	}
	if !graphViaIR.DippinValidated {
		t.Errorf("IR path did not mark DippinValidated")
	}
}

// loadFileDirectivesFixture reads a fixture .dip under testdata/filedirectives
// and returns (source, absolute filename). Absolute so resolution must anchor
// on the .dip's own directory, never the test process cwd.
func loadFileDirectivesFixture(t *testing.T, name string) (string, string) {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", "filedirectives", name))
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(data), abs
}

func TestLoadDippinWorkflow_ResolvesCommandFileAndPromptFile(t *testing.T) {
	source, filename := loadFileDirectivesFixture(t, "workflow.dip")

	graph, _, err := LoadDippinWorkflow(source, filename)
	if err != nil {
		t.Fatalf("LoadDippinWorkflow: %v", err)
	}

	tool := graph.Nodes["RunScript"]
	if tool == nil {
		t.Fatal("node RunScript missing from graph")
	}
	if got := tool.Attrs["tool_command"]; !strings.Contains(got, "command from file directive fixture") {
		t.Errorf("tool_command not resolved from command_file; got %q", got)
	}

	start := graph.Nodes["Start"]
	if start == nil {
		t.Fatal("node Start missing from graph")
	}
	if got := start.Attrs["prompt"]; !strings.Contains(got, "prompt from file directive fixture") {
		t.Errorf("prompt not resolved from prompt_file; got %q", got)
	}
}

func TestLoadDippinWorkflow_ResolvesRelativeToFileDirNotCwd(t *testing.T) {
	source, filename := loadFileDirectivesFixture(t, "workflow.dip")

	// Change to an unrelated cwd: resolution must anchor on the .dip's dir.
	t.Chdir(t.TempDir())

	graph, _, err := LoadDippinWorkflow(source, filename)
	if err != nil {
		t.Fatalf("LoadDippinWorkflow from foreign cwd: %v", err)
	}
	if got := graph.Nodes["RunScript"].Attrs["tool_command"]; !strings.Contains(got, "command from file directive fixture") {
		t.Errorf("tool_command not resolved from foreign cwd; got %q", got)
	}
}

func TestLoadDippinWorkflow_MissingDirectiveFileFailsLoudly(t *testing.T) {
	source, filename := loadFileDirectivesFixture(t, "workflow_missing.dip")

	_, _, err := LoadDippinWorkflow(source, filename)
	if err == nil {
		t.Fatal("expected error for missing command_file target")
	}
	if !strings.Contains(err.Error(), "RunScript") {
		t.Errorf("error should name the node ID RunScript: %v", err)
	}
	if !strings.Contains(err.Error(), "scripts/does_not_exist.sh") {
		t.Errorf("error should name the referenced path: %v", err)
	}
}

func TestLoadDippinWorkflowFromIR_NilWorkflow(t *testing.T) {
	_, _, err := LoadDippinWorkflowFromIR(nil, "x.dip")
	if err == nil {
		t.Fatal("expected error on nil workflow")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "nil") {
		t.Errorf("error should mention nil workflow: %v", err)
	}
}
