// ABOUTME: End-to-end witness for #714 — a tool node that interpolates a
// ABOUTME: tool_command-safe ctx key restored from a tampered checkpoint must fail
// ABOUTME: closed on resume, never executing the injected shell text.
package handlers

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"testing"

	agentexec "github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/pipeline"
)

// TestResumeRejectsInjectedSafeKeyInToolCommand plants a checkpoint whose
// human_response — a tool_command-safe allowlisted key — carries shell text that
// would create a sentinel file if executed. On resume, a tool node that
// interpolates ${ctx.human_response} must be rejected (the value is
// checkpoint-restored, not live human input), so the sentinel is never created.
// This is the confused-deputy scenario from #714: a jailed agent plants the
// checkpoint, an unjailed tool node would otherwise run its contents.
func TestResumeRejectsInjectedSafeKeyInToolCommand(t *testing.T) {
	if _, err := osexec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	t.Setenv("TRACKER_AUDIT_DIR", t.TempDir())
	workdir := t.TempDir()
	artifactDir := filepath.Join(workdir, ".tracker", "runs")
	cpPath := filepath.Join(workdir, "checkpoint.json")
	sentinel := filepath.Join(workdir, "PWNED")

	// A tampered checkpoint sets the allowlisted key to shell-injection text.
	cp := &pipeline.Checkpoint{
		RunID:          "r714",
		CompletedNodes: []string{},
		RetryCounts:    map[string]int{},
		Context:        map[string]string{pipeline.ContextKeyHumanResponse: "x; touch " + sentinel + " #"},
	}
	if err := pipeline.SaveCheckpoint(cp, cpPath); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	// A single tool START node that interpolates the safe key into its command.
	dot := `digraph t {
		First [shape=parallelogram, tool_command="echo ${ctx.human_response}"];
		Exit [shape=Msquare];
		First -> Exit;
	}`
	graph, err := pipeline.ParseDOT(dot)
	if err != nil {
		t.Fatalf("ParseDOT: %v", err)
	}
	graph.StartNode = "First"

	registry := pipeline.NewHandlerRegistry()
	registry.Register(NewExitHandler())
	registry.Register(NewToolHandler(agentexec.NewLocalEnvironment(workdir)))

	engine := pipeline.NewEngine(graph, registry,
		pipeline.WithArtifactDir(artifactDir), pipeline.WithCheckpointPath(cpPath))
	result, runErr := engine.Run(context.Background())

	// Witness: the injected command never ran.
	if _, statErr := os.Stat(sentinel); statErr == nil {
		t.Fatal("injected shell text executed — sentinel file was created (#714 not enforced)")
	}
	// The node must have failed closed rather than running the command.
	if runErr == nil && result != nil && result.Status == pipeline.OutcomeSuccess {
		t.Fatalf("resume succeeded running a checkpoint-restored safe key; want fail-closed (status=%q err=%v)", result.Status, runErr)
	}
}
