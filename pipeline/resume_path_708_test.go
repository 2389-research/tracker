// ABOUTME: Regression guard for issue #708 — a resumed checkpoint's run ID and completed-node IDs are
// ABOUTME: path elements: an escaping run ID fails the resume, and an escaping node ID is never read.
package pipeline

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// fatalfer is the part of *testing.T and *rapid.T the helpers need.
type fatalfer interface {
	Helper()
	Fatalf(format string, args ...any)
}

// resumeWithRunID saves a checkpoint carrying runID under root and resumes the
// probe graph from it, with the artifact base four directories deep so an
// escape stays inside root where dirsOutside can see it. It returns the base,
// the probe, and Run's error.
func resumeWithRunID(t fatalfer, root, runID string) (string, *runDirProbe, error) {
	t.Helper()
	base := filepath.Join(root, "w1", "w2", "w3", "w4", ".tracker", "runs")
	cpPath := filepath.Join(root, "checkpoint.json")
	cp := &Checkpoint{RunID: runID, CompletedNodes: []string{}, RetryCounts: map[string]int{}, Context: map[string]string{}}
	if err := SaveCheckpoint(cp, cpPath); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	probe := newRunDirProbe()
	g, reg := runDirGraph(probe)
	_, err := NewEngine(g, reg, WithArtifactDir(base), WithCheckpointPath(cpPath)).Run(context.Background())
	return base, probe, err
}

// within reports whether path is dir or inside it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// dirsOutside lists the directories under root that are neither inside base
// nor one of its ancestors: anything a run created outside its artifact base.
func dirsOutside(t fatalfer, root, base string) []string {
	t.Helper()
	var outside []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && !within(path, base) && !within(base, path) {
			outside = append(outside, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return outside
}

func TestResumeRejectsCheckpointRunIDOutsideBase(t *testing.T) {
	t.Setenv("TRACKER_AUDIT_DIR", t.TempDir())
	for _, runID := range []string{"../x", "a/b", `a\b`, "..", ".", "/abs"} {
		t.Run(strings.NewReplacer("/", "slash", `\`, "backslash", ".", "dot").Replace(runID), func(t *testing.T) {
			root := t.TempDir()
			base, probe, err := resumeWithRunID(t, root, runID)

			if err == nil || !strings.Contains(err.Error(), "runID") {
				t.Fatalf("resume from a checkpoint with run ID %q: err = %v, want a runID error", runID, err)
			}
			if len(probe.dirs) != 0 {
				t.Errorf("nodes ran before the resume was refused: %v", probe.dirs)
			}
			if outside := dirsOutside(t, root, base); len(outside) > 0 {
				t.Errorf("run ID %q created directories outside the artifact base: %v", runID, outside)
			}
		})
	}
}

func TestResumeRunIDNeverEscapesArtifactBase(t *testing.T) {
	t.Setenv("TRACKER_AUDIT_DIR", t.TempDir())
	rapid.Check(t, func(rt *rapid.T) {
		runID := rapid.StringMatching(`^[./\\a]{1,12}$`).Draw(rt, "runID")
		root, err := os.MkdirTemp(t.TempDir(), "resume")
		if err != nil {
			rt.Fatalf("temp dir: %v", err)
		}

		base, _, runErr := resumeWithRunID(rt, root, runID)

		if outside := dirsOutside(rt, root, base); len(outside) > 0 {
			rt.Fatalf("run ID %q created directories outside the artifact base: %v", runID, outside)
		}
		if validateRunID(runID) != nil {
			if runErr == nil {
				rt.Fatalf("run ID %q fails validateRunID, but the resume ran", runID)
			}
			return
		}
		if runErr != nil {
			rt.Fatalf("valid run ID %q: %v", runID, runErr)
		}
		if info, statErr := os.Stat(filepath.Join(base, runID)); statErr != nil || !info.IsDir() {
			rt.Fatalf("valid run ID %q: run dir %s missing: %v", runID, filepath.Join(base, runID), statErr)
		}
	})
}

func TestCompactSummaryHighSkipsCompletedNodeIDsOutsideRunDir(t *testing.T) {
	root := t.TempDir()
	artifactDir := filepath.Join(root, "w1", "w2", "runs")
	escape := "../../../planted"
	for path, body := range map[string]string{
		filepath.Join(artifactDir, "r708", "Build", "response.md"): "build output",
		filepath.Join(artifactDir, "r708", escape, "response.md"):  "PLANTED",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	got := compactSummaryHigh(NewPipelineContext(), []string{"Build", escape}, artifactDir, "r708")

	if got["summary.Build"] != "build output" {
		t.Errorf("summary.Build = %q, want the Build node's response", got["summary.Build"])
	}
	for key, value := range got {
		if strings.Contains(value, "PLANTED") {
			t.Errorf("%s holds the response.md planted outside the run dir", key)
		}
	}
}
