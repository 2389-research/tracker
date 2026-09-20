// ABOUTME: Integration test: drives the real pipeline engine end to end and asserts
// ABOUTME: the Reporter turns a genuine event stream into herdr working/idle/release calls.
package herdr

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// passthroughHandler succeeds for any node, so a minimal graph runs to a terminal
// without agents, tools, or an interviewer — the real engine still emits the
// pipeline-started and top-level terminal events the Reporter keys on.
type passthroughHandler struct{ name string }

func (h passthroughHandler) Name() string { return h.name }

func (h passthroughHandler) Execute(_ context.Context, _ *pipeline.Node, _ *pipeline.PipelineContext) (pipeline.Outcome, error) {
	return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
}

// TestReporterDrivenByRealEngine is the integration proof that the Reporter's
// event contract matches what the engine actually emits: the unit tests assert
// on hand-built PipelineEvents, this runs a real engine over a real graph.
func TestReporterDrivenByRealEngine(t *testing.T) {
	const dot = `digraph mini {
		start [shape=Mdiamond label="Start"];
		done [shape=Msquare label="Done"];
		start -> done;
	}`
	g, err := pipeline.ParseDOT(dot)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	reg := pipeline.NewHandlerRegistry()
	for _, name := range []string{"start", "exit"} {
		reg.Register(passthroughHandler{name: name})
	}

	// Keep every run artifact inside the test's temp dir.
	tmp := t.TempDir()
	t.Setenv("TRACKER_AUDIT_DIR", tmp)

	fr := &fakeRunner{}
	reporter := newReporter(testEnv(), true, fr.run)

	engine := pipeline.NewEngine(g, reg,
		pipeline.WithPipelineEventHandler(reporter),
		pipeline.WithArtifactDir(filepath.Join(tmp, "artifacts")),
		pipeline.WithCheckpointPath(filepath.Join(tmp, "checkpoint.json")),
	)
	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("engine run: %v", err)
	}
	reporter.Release()

	calls := fr.snapshot()

	// The genuine event stream must drive exactly working then idle.
	if states := reportedStates(calls); !equalStrings(states, []string{"working", "idle"}) {
		t.Fatalf("state sequence from real engine = %v, want [working idle]", states)
	}

	// Teardown must release lifecycle authority, and release carries no --seq.
	last := calls[len(calls)-1]
	if len(last.args) < 2 || last.args[1] != "release-agent" {
		t.Fatalf("last call = %v, want a release-agent call", last.args)
	}
	if _, ok := flagVal(last.args, "--seq"); ok {
		t.Errorf("release-agent must not carry --seq: %v", last.args)
	}
}
