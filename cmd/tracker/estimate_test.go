// ABOUTME: Tests the `tracker estimate` command wiring and output formatting.
package main

import (
	"bytes"
	"strings"
	"testing"

	tracker "github.com/2389-research/tracker"
)

func TestEstimate_SubcommandRecognized(t *testing.T) {
	if subcommandMap["estimate"] != modeEstimate {
		t.Fatalf("`estimate` not mapped to modeEstimate: %v", subcommandMap["estimate"])
	}
}

func TestPrintEstimate(t *testing.T) {
	var buf bytes.Buffer
	printEstimate(&buf, "myflow", &tracker.RunEstimate{
		Steps: 10, AgentNodes: 3,
		Models: []string{"claude-opus-4-6", "gpt-5.2"},
		LowUSD: 0.5, HighUSD: 5.0, ExpectedUSD: 2.0,
	})
	out := buf.String()
	for _, want := range []string{"myflow", "Steps:", "10", "claude-opus-4-6", "gpt-5.2", "$0.50", "$5.00", "$2.00"} {
		if !strings.Contains(out, want) {
			t.Errorf("estimate output missing %q:\n%s", want, out)
		}
	}
}

func TestExecuteEstimate_BuiltinWorkflow(t *testing.T) {
	// A built-in resolves by bare name and produces a non-zero estimate.
	if err := executeEstimate(runConfig{pipelineFile: "ask_and_execute"}); err != nil {
		t.Fatalf("executeEstimate(ask_and_execute): %v", err)
	}
}
