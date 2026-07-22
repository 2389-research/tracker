// ABOUTME: Verifies Config.GitArtifacts wires WithGitArtifacts into the engine so
// ABOUTME: the artifact run dir becomes a git repo with per-terminal-node commits —
// ABOUTME: the seam the consolidation's branch-per-run / PR delivery depends on.
package tracker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// noopCompleter satisfies agent.Completer so Run's eager client build succeeds
// even though this completer-free probe never calls it.
type noopCompleter struct{}

func (noopCompleter) Complete(_ context.Context, _ *llm.Request) (*llm.Response, error) {
	return &llm.Response{
		Message:      llm.AssistantMessage("noop"),
		FinishReason: llm.FinishReason{Reason: "stop"},
	}, nil
}

// gitArtifactProbe is completer-free: bare start/exit + a printf tool node, so it
// runs with no LLM provider configured.
const gitArtifactProbe = `workflow git_artifacts_probe
  start: Start
  exit: Done

  agent Start
    label: Start

  tool Emit
    label: Emit
    timeout: 10s
    command:
      printf 'ok'

  agent Done
    label: Done

  edges
    Start -> Emit
    Emit -> Done
`

func TestConfigGitArtifactsEnablesCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}

	cfg := Config{
		WorkingDir:   t.TempDir(),
		ArtifactDir:  t.TempDir(),
		GitArtifacts: true,
		LLMClient:    noopCompleter{},
	}

	result, err := Run(context.Background(), gitArtifactProbe, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !isSuccess(result.Status) {
		t.Fatalf("run did not succeed: status=%q", result.Status)
	}

	runDir := result.ArtifactRunDir
	if runDir == "" {
		t.Fatal("ArtifactRunDir empty; GitArtifacts wiring did not take effect")
	}
	if _, err := os.Stat(filepath.Join(runDir, ".git")); err != nil {
		t.Fatalf("expected a git repo at artifact run dir %s: %v", runDir, err)
	}

	out, err := exec.Command("git", "-C", runDir, "rev-list", "--count", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-list: %v", err)
	}
	if strings.TrimSpace(string(out)) == "0" {
		t.Fatal("expected at least one artifact commit, got 0")
	}
}

func isSuccess(status string) bool {
	return status == "success" || status == "validation_overridden"
}
