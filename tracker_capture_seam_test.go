// ABOUTME: Tests the Config.Capture seam — the library produces the same on-disk run capture the CLI does, without the caller hand-wiring the three JSONL handlers.
// ABOUTME: Guards the double-log trap: LLM calls are logged exactly once even though capture wires EventHandler + AgentEvents + LLMTrace to one handler.
package tracker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// TestConfigCaptureProducesArtifactsWithoutHandlerWiring is the TDD anchor for
// the embedder seam: an embedder that sets Config.Capture and wires no event
// handlers of its own still gets run.json, spec artifacts, and an
// identity-bearing activity.jsonl — exactly what the CLI produces.
//
// It runs through a real llm.Client (tracingStubAdapter) so the trace path is
// live and the SessionOwned de-dup rule is actually exercised: a session
// re-emits its trace as agent llm_* events, so wiring the raw trace to the same
// handler would log every call twice. The adapter's own call count is the
// oracle for "exactly once".
func TestConfigCaptureProducesArtifactsWithoutHandlerWiring(t *testing.T) {
	adapter := &tracingStubAdapter{}
	client, err := llm.NewClient(
		llm.WithProvider(adapter),
		llm.WithDefaultProvider("anthropic"),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	workDir := t.TempDir()

	// No EventHandler / AgentEvents / LLMTrace — Config.Capture wires them.
	result, err := Run(context.Background(), captureDip, Config{
		Format:     "dip",
		WorkingDir: workDir,
		LLMClient:  client,
		Model:      "stub-model",
		Provider:   "anthropic",
		Capture:    &CaptureConfig{},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	runDir := filepath.Join(workDir, ".tracker", "runs", result.RunID)
	if result.ArtifactRunDir != runDir {
		t.Errorf("ArtifactRunDir = %q, want %q", result.ArtifactRunDir, runDir)
	}

	t.Run("spec artifacts and run.json are on disk", func(t *testing.T) {
		for _, name := range []string{
			pipeline.RunManifestFile,
			pipeline.SpecSourceFile,
			pipeline.SpecIRFile,
			"activity.jsonl",
		} {
			if _, statErr := os.Stat(filepath.Join(runDir, name)); statErr != nil {
				t.Errorf("expected %s in run dir: %v", name, statErr)
			}
		}
	})

	t.Run("activity.jsonl carries session identity", func(t *testing.T) {
		entries := readRunLog(t, workDir)
		var sawSession bool
		for _, e := range entries {
			if e.Source == "agent" && sessionScopedType(e.Type) && e.SessionID != "" {
				sawSession = true
			}
		}
		if !sawSession {
			t.Error("no session-scoped agent event carried a session_id")
		}
	})

	t.Run("every LLM call is logged exactly once", func(t *testing.T) {
		data, readErr := os.ReadFile(filepath.Join(runDir, pipeline.RunManifestFile))
		if readErr != nil {
			t.Fatalf("read run.json: %v", readErr)
		}
		var m pipeline.RunManifest
		if jsonErr := json.Unmarshal(data, &m); jsonErr != nil {
			t.Fatalf("unmarshal run.json: %v", jsonErr)
		}
		adapter.mu.Lock()
		want := adapter.calls
		adapter.mu.Unlock()
		if m.Totals.LLMCalls != want {
			t.Errorf("run.json llm_calls = %d, adapter streamed %d call(s) — a mismatch means the trace was double-logged or dropped", m.Totals.LLMCalls, want)
		}

		// run.json's LLMCalls is call_id-deduped, so it is duplication-robust and
		// stays correct even if the raw log doubles. Assert directly on the raw
		// activity.jsonl lines: the capture trace observer drops SessionOwned
		// events, so a session's own llm trace must NOT appear as source:"llm"
		// finish lines (those are duplicates of the agent llm_finish events). If
		// the SessionOwned filter regresses, these reappear and this fails even
		// though the manifest count does not.
		var rawLLMFinish int
		for _, e := range readRunLog(t, workDir) {
			if e.Source == "llm" && e.Type == string(llm.TraceFinish) {
				rawLLMFinish++
			}
		}
		if rawLLMFinish != 0 {
			t.Errorf("activity.jsonl has %d source:\"llm\" finish line(s); want 0 — the SessionOwned de-dup filter dropped, so the session trace is double-logged", rawLLMFinish)
		}
	})
}
