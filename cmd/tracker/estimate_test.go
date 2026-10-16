// ABOUTME: Tests the `tracker estimate` command wiring and output formatting.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	tracker "github.com/2389-research/tracker"
	"github.com/2389-research/tracker/internal/chatgptauth"
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

func TestPrintEstimateSubscriptionLine(t *testing.T) {
	var buf bytes.Buffer
	printEstimate(&buf, "myflow", &tracker.RunEstimate{
		Steps: 5, AgentNodes: 1, SubscriptionNodes: 1,
		Models: []string{"gpt-5.6-sol"},
	})
	out := buf.String()
	want := "Subscription: 1 openai node(s) on your ChatGPT subscription — $0 here; dollar ceilings don't count them."
	if !strings.Contains(out, want) {
		t.Errorf("estimate output missing subscription line:\n%s\nwant substring: %q", out, want)
	}
}

func TestPrintEstimateNoSubscriptionLineWhenZero(t *testing.T) {
	var buf bytes.Buffer
	printEstimate(&buf, "myflow", &tracker.RunEstimate{
		Steps: 5, AgentNodes: 1, SubscriptionNodes: 0,
	})
	if strings.Contains(buf.String(), "Subscription:") {
		t.Errorf("estimate output printed a subscription line with SubscriptionNodes=0:\n%s", buf.String())
	}
}

// TestEstimateLoadsConfigEnvOnly pins the brief's contract: `tracker estimate`
// must see the config .env (where `tracker setup` writes TRACKER_OPENAI_AUTH)
// but must never load a project .env from a casually-opened checkout — the
// same contract executeVersion already holds (TestVersionDoesNotLoadProjectEnv).
func TestEstimateLoadsConfigEnvOnly(t *testing.T) {
	f := newEnvFixture(t)
	unsetEnvForTest(t, "ANTHROPIC_API_KEY")
	unsetEnvForTest(t, chatgptauth.EnvVar)
	f.writeProject(t, "ANTHROPIC_API_KEY=proj-secret\n")
	f.writeConfig(t, "TRACKER_OPENAI_AUTH=chatgpt\n")
	t.Chdir(f.workdir)

	if err := executeEstimate(runConfig{pipelineFile: "ask_and_execute"}); err != nil {
		t.Fatalf("executeEstimate: %v", err)
	}
	if got := os.Getenv("ANTHROPIC_API_KEY"); got != "" {
		t.Errorf("tracker estimate loaded the cwd project .env (ANTHROPIC_API_KEY=%q)", got)
	}
	if got := os.Getenv(chatgptauth.EnvVar); got != "chatgpt" {
		t.Errorf("tracker estimate did not load the config .env (%s=%q)", chatgptauth.EnvVar, got)
	}
}
