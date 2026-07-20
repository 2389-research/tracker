// ABOUTME: Tests Config.ToolSafety wiring into the tool handler (#478).
package tracker

import (
	"context"
	"testing"

	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/pipeline/handlers"
)

const toolDip = `workflow tooltest
  start: T
  exit: Done

  tool T
    command:
      echo hello

  agent Done
    label: Done

  edges
    T -> Done
`

// TestConfig_ToolSafetyDenylistApplies proves Config.ToolSafety is threaded into
// the tool handler: adding "echo" to the denylist blocks a tool node that runs
// it, while the same pipeline succeeds without the restriction.
func TestConfig_ToolSafetyDenylistApplies(t *testing.T) {
	run := func(safety *handlers.ToolHandlerConfig) (string, error) {
		res, err := Run(context.Background(), toolDip, Config{
			Format:     "dip",
			WorkingDir: t.TempDir(),
			LLMClient:  successStub(),
			ToolSafety: safety,
		})
		status := ""
		if res != nil {
			status = res.Status
		}
		return status, err
	}

	// Baseline: echo runs, the pipeline succeeds.
	if status, err := run(nil); err != nil || !pipeline.TerminalStatus(status).IsSuccess() {
		t.Fatalf("baseline (no tool safety) should succeed; status=%q err=%v", status, err)
	}
	// Denylisting "echo *" blocks the tool node — it fails as a handler error.
	if status, err := run(&handlers.ToolHandlerConfig{DenylistAdd: []string{"echo *"}}); err == nil && pipeline.TerminalStatus(status).IsSuccess() {
		t.Fatalf("denylisting echo should block the tool node; status=%q err=%v", status, err)
	}
}
