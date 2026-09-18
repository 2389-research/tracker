// ABOUTME: Library-level end-to-end for writable_paths_mode: prefer (#648) on a
// ABOUTME: host without Landlock — the node RUNS and the degrade is recorded on every surface (spec C5, C7).
package tracker

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/agent"
	execpkg "github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

type preferStub struct{}

func (preferStub) Complete(_ context.Context, _ *llm.Request) (*llm.Response, error) {
	return &llm.Response{
		Message:      llm.AssistantMessage("committed"),
		FinishReason: llm.FinishReason{Reason: "stop"},
		Usage:        llm.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}, nil
}

const preferDip = `workflow prefer
  start: Commit
  exit: Done

  agent Commit
    label: Commit
    writable_paths: .git/**, .ai/**
    params:
      writable_paths_mode: prefer
    prompt:
      Commit the work.

  agent Done
    label: Done

  edges
    Commit -> Done
`

// TestRun_C7_PreferDegradesEndToEnd: on a host whose Landlock probe fails, a
// prefer node runs to success, activity.jsonl carries exactly one jail_degraded
// line with the declared globs, run.json lists the node under
// jail_degraded_nodes, and the trace entry's stats carry jail=degraded.
// (The diagnose suggestion is pinned by TestDiagnose_C7_JailDegradedSuggestion.)
func TestRun_C7_PreferDegradesEndToEnd(t *testing.T) {
	if err := execpkg.ProbeLandlock(); err == nil {
		t.Skip("Landlock available on this host; the prefer degrade path is unreachable")
	}
	workDir := t.TempDir()
	runsDir := filepath.Join(workDir, ".tracker", "runs")
	handler := pipeline.NewJSONLEventHandler(runsDir)
	var degradeEvents []pipeline.PipelineEvent
	rec := pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
		if evt.Type == pipeline.EventJailDegraded {
			degradeEvents = append(degradeEvents, evt)
		}
		handler.HandlePipelineEvent(evt)
	})

	result, err := Run(context.Background(), preferDip, Config{
		Format:       "dip",
		WorkingDir:   workDir,
		LLMClient:    preferStub{},
		EventHandler: rec,
		AgentEvents:  agent.EventHandlerFunc(handler.WriteAgentEvent),
	})
	if err != nil {
		t.Fatalf("Run: %v (a prefer node must run unjailed, not refuse)", err)
	}
	if err := handler.Close(); err != nil {
		t.Fatalf("close log: %v", err)
	}
	if !pipeline.TerminalStatus(result.Status).IsSuccess() {
		t.Fatalf("status = %q, want success", result.Status)
	}

	// Exactly one jail_degraded event, naming the node and the declared globs,
	// with the UNJAILED copy.
	if len(degradeEvents) != 1 {
		t.Fatalf("jail_degraded emitted %d times, want 1", len(degradeEvents))
	}
	evt := degradeEvents[0]
	if evt.NodeID != "Commit" || evt.Jail == nil || len(evt.Jail.DeclaredGlobs) != 2 {
		t.Fatalf("event = %+v", evt)
	}
	if !strings.Contains(evt.Message, "UNJAILED") {
		t.Errorf("warning does not say UNJAILED: %s", evt.Message)
	}

	// run.json.
	runDir := filepath.Join(runsDir, result.RunID)
	m, err := pipeline.AssembleRunManifest(runDir, result.RunID)
	if err != nil {
		t.Fatalf("AssembleRunManifest: %v", err)
	}
	if len(m.JailDegradedNodes) != 1 || m.JailDegradedNodes[0] != "Commit" {
		t.Errorf("jail_degraded_nodes = %v, want [Commit]", m.JailDegradedNodes)
	}
	for _, n := range m.Nodes {
		switch n.ID {
		case "Commit":
			if n.Jail != pipeline.JailDegraded {
				t.Errorf("Commit.jail = %q, want degraded", n.Jail)
			}
		default:
			if n.Jail != "" {
				t.Errorf("%s.jail = %q, want empty", n.ID, n.Jail)
			}
		}
	}

	// Trace entry.
	var found bool
	for _, e := range result.Trace.Entries {
		if e.NodeID == "Commit" && e.Stats != nil {
			found = true
			if e.Stats.Jail != pipeline.JailDegraded {
				t.Errorf("trace stats.jail = %q, want degraded", e.Stats.Jail)
			}
		}
	}
	if !found {
		t.Error("no trace entry with stats for Commit")
	}
}
