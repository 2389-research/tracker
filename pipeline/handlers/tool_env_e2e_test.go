// ABOUTME: Engine-level test for run-identity env vars in tool subprocesses (#323).
// ABOUTME: A codergen node (fake backend) writes response.md; a tool node reads it via $TRACKER_RUN_DIR.
package handlers

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	agentexec "github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/pipeline"
)

// TestToolReadsUpstreamResponseViaRunDir runs a two-node pipeline: a codergen
// node backed by a fake completer, then a tool node that cats the upstream
// node's response.md using $TRACKER_RUN_DIR. This is the acceptance test for
// issue #323 — the tool_access:none agent → tool data flow without any
// ls -dt mtime heuristic.
func TestToolReadsUpstreamResponseViaRunDir(t *testing.T) {
	if _, err := osexec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	workdir := t.TempDir()
	artifactDir := filepath.Join(workdir, ".tracker", "runs")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}

	dot := `digraph t {
		Start [shape=Mdiamond];
		Agent [shape=box, prompt="say the magic word"];
		ReadBack [shape=parallelogram, tool_command="cat \"$TRACKER_RUN_DIR/Agent/response.md\""];
		Exit [shape=Msquare];
		Start -> Agent;
		Agent -> ReadBack;
		ReadBack -> Exit;
	}`
	graph, err := pipeline.ParseDOT(dot)
	if err != nil {
		t.Fatalf("ParseDOT: %v", err)
	}

	codergen := NewCodergenHandler(&fakeCompleter{responseText: "magic-word-xyzzy"}, workdir)
	codergen.env = agentexec.NewLocalEnvironment(workdir)

	registry := pipeline.NewHandlerRegistry()
	registry.Register(NewStartHandler())
	registry.Register(NewExitHandler())
	registry.Register(codergen)
	registry.Register(NewToolHandler(agentexec.NewLocalEnvironment(workdir)))

	engine := pipeline.NewEngine(graph, registry, pipeline.WithArtifactDir(artifactDir))
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine.Run: %v", err)
	}
	if result.Status != pipeline.OutcomeSuccess {
		t.Fatalf("pipeline status = %q, want success", result.Status)
	}

	stdout, ok := result.Context[pipeline.ContextKeyToolStdout]
	if !ok {
		t.Fatal("tool_stdout missing from final context")
	}
	if !strings.Contains(stdout, "magic-word-xyzzy") {
		t.Errorf("tool_stdout = %q, want it to contain the upstream agent's response", stdout)
	}
}
