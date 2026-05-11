// ABOUTME: End-to-end test verifying .dipx bundle identity flows through
// ABOUTME: every audit surface — Result, Checkpoint, activity.jsonl, RunSummary.
package tracker

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/internal/dipxtest"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// TestE2E_DipxIdentityFlowsThroughAllAuditSurfaces drives a real .dipx bundle
// through tracker.Run and asserts the content-addressed identity appears on
// every audit surface: Result.BundleIdentity, Checkpoint.BundleIdentity,
// EVERY line of activity.jsonl, and RunSummary.BundleIdentity (via ListRuns).
//
// The pipeline is the minimal start→exit passthrough produced by
// dipxtest.MinimalDip, so it completes without any LLM calls (the bare
// codergen nodes get the passthrough start/exit handlers — see
// CLAUDE.md § ensureStartExitNodes). The test runs to completion in
// well under a second.
//
// This pins the integration end-to-end so that a future regression on
// any one surface (a stamping bypass, an unwired registry, a forgotten
// Result mirror) is caught here even if the surface-level unit tests
// continue to pass.
func TestE2E_DipxIdentityFlowsThroughAllAuditSurfaces(t *testing.T) {
	// 1. Pack a real .dipx bundle from MinimalDip source.
	srcDir := t.TempDir()
	entryPath := filepath.Join(srcDir, "entry.dip")
	dipSource := dipxtest.MinimalDip("e2e_test", "start", "exit")
	if err := os.WriteFile(entryPath, []byte(dipSource), 0o644); err != nil {
		t.Fatalf("write entry.dip: %v", err)
	}
	bundlePath := dipxtest.PackTestBundle(t, entryPath)

	// 2. Compute the canonical bundle identity by opening the .dipx —
	//    this is exactly what the CLI loader does at run start, so
	//    we're feeding the same value into tracker.Run.
	_, _, info, err := pipeline.LoadDipxBundle(context.Background(), bundlePath)
	if err != nil {
		t.Fatalf("LoadDipxBundle: %v", err)
	}
	wantIdentity := info.Identity
	if !strings.HasPrefix(wantIdentity, "sha256:") || len(wantIdentity) != len("sha256:")+64 {
		t.Fatalf("unexpected identity shape: %q", wantIdentity)
	}

	// 3. Stage workdir + artifact dir so ListRuns can find the run later.
	//    Mirrors the CLI layout: <workdir>/.tracker/runs/<runID>/{checkpoint,activity.jsonl}.
	workdir := t.TempDir()
	artifactDir := filepath.Join(workdir, ".tracker", "runs")

	// 4. Wire the JSONL activity log as the pipeline event handler so
	//    activity.jsonl gets written. This matches what the CLI does
	//    inside run.go. We also set the bundle identity on the handler
	//    explicitly so any agent/llm writes (none expected for this
	//    minimal pipeline) would also be stamped — belt-and-suspenders.
	activityLog := pipeline.NewJSONLEventHandler(artifactDir)
	activityLog.SetBundleIdentity(wantIdentity)

	// 5. Run via the library API. The start/exit nodes are bare codergen
	//    agents with no prompt, so ensureStartExitNodes assigns passthrough
	//    handlers that do not invoke any LLM. A stub completer is still
	//    required because resolveCompleter builds an LLM client up-front
	//    (matching the same pattern other library tests use, e.g.
	//    TestRun_DipPipeline) so the test does not depend on env API keys.
	stub := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("ignored"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}
	cfg := Config{
		Format:         "dip",
		WorkingDir:     workdir,
		ArtifactDir:    artifactDir,
		BundleIdentity: wantIdentity,
		EventHandler:   activityLog,
		LLMClient:      stub,
	}
	result, err := Run(context.Background(), dipSource, cfg)
	// Close the activity log to flush any buffered writes before reading
	// activity.jsonl. JSONLEventHandler.Close is not idempotent, so we
	// only call it once here (no defer).
	if cerr := activityLog.Close(); cerr != nil && err == nil {
		t.Fatalf("activityLog.Close: %v", cerr)
	}
	if err != nil {
		t.Fatalf("tracker.Run: %v", err)
	}

	if result.Status != "success" {
		t.Fatalf("pipeline status = %q, want success", result.Status)
	}
	if result.RunID == "" {
		t.Fatal("empty RunID")
	}
	if result.ArtifactRunDir == "" {
		t.Fatal("empty ArtifactRunDir — needed to locate audit surfaces")
	}

	// Surface 1: tracker.Result.BundleIdentity (the returned Result).
	t.Run("Surface1_ResultBundleIdentity", func(t *testing.T) {
		if result.BundleIdentity != wantIdentity {
			t.Errorf("Result.BundleIdentity = %q, want %q", result.BundleIdentity, wantIdentity)
		}
	})

	// Surface 2: Checkpoint.BundleIdentity (the persisted checkpoint).
	t.Run("Surface2_CheckpointBundleIdentity", func(t *testing.T) {
		cpPath := filepath.Join(result.ArtifactRunDir, "checkpoint.json")
		cp, err := pipeline.LoadCheckpoint(cpPath)
		if err != nil {
			t.Fatalf("LoadCheckpoint(%s): %v", cpPath, err)
		}
		if cp.BundleIdentity != wantIdentity {
			t.Errorf("Checkpoint.BundleIdentity = %q, want %q", cp.BundleIdentity, wantIdentity)
		}
	})

	// Surface 3: every line of activity.jsonl carries the identity.
	t.Run("Surface3_ActivityJSONLEveryLine", func(t *testing.T) {
		logPath := filepath.Join(result.ArtifactRunDir, "activity.jsonl")
		logBytes, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("read activity.jsonl: %v", err)
		}
		trimmed := strings.TrimSpace(string(logBytes))
		if trimmed == "" {
			t.Fatal("activity.jsonl is empty — expected at least one pipeline event")
		}
		lines := strings.Split(trimmed, "\n")
		if len(lines) == 0 {
			t.Fatal("no lines in activity.jsonl")
		}
		for i, line := range lines {
			if line == "" {
				continue
			}
			var entry map[string]any
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				t.Fatalf("line %d not valid JSON: %v\nline: %s", i, err, line)
			}
			got, _ := entry["bundle_identity"].(string)
			if got != wantIdentity {
				t.Errorf("line %d: bundle_identity = %q, want %q\n  full line: %s",
					i, got, wantIdentity, line)
			}
		}
		t.Logf("verified bundle_identity on %d activity.jsonl lines", len(lines))
	})

	// Surface 4: RunSummary.BundleIdentity (via tracker.ListRuns).
	t.Run("Surface4_RunSummaryViaListRuns", func(t *testing.T) {
		runs, err := ListRuns(workdir, AuditConfig{LogWriter: io.Discard})
		if err != nil {
			t.Fatalf("ListRuns: %v", err)
		}
		if len(runs) == 0 {
			t.Fatal("ListRuns returned no runs")
		}
		var found *RunSummary
		for i := range runs {
			if runs[i].RunID == result.RunID {
				found = &runs[i]
				break
			}
		}
		if found == nil {
			t.Fatalf("RunSummary for RunID %q not found; got %d runs", result.RunID, len(runs))
		}
		if found.BundleIdentity != wantIdentity {
			t.Errorf("RunSummary.BundleIdentity = %q, want %q", found.BundleIdentity, wantIdentity)
		}
	})
}
