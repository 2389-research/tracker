// ABOUTME: Regression guard for issue #686 — the per-run artifact dir (TRACKER_RUN_DIR)
// ABOUTME: must exist before the FIRST node runs: fresh runs, resumes, and subgraph-inherited dirs.
package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// runDirProbe records, per node, the InternalKeyArtifactDir the node saw and
// whether that directory existed while the node was executing.
type runDirProbe struct {
	dirs   map[string]string
	exists map[string]bool
}

func newRunDirProbe() *runDirProbe {
	return &runDirProbe{dirs: map[string]string{}, exists: map[string]bool{}}
}

func (p *runDirProbe) execute(_ context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
	dir, _ := pctx.GetInternal(InternalKeyArtifactDir)
	p.dirs[node.ID] = dir
	if dir != "" {
		info, err := os.Stat(dir)
		p.exists[node.ID] = err == nil && info.IsDir()
	}
	return Outcome{Status: OutcomeSuccess}, nil
}

// runDirGraph builds First -> Second -> End where First is the graph's start
// node AND a real (probing) handler, not a passthrough — the issue's
// `start: First` tool-node shape. A passthrough start node hid the bug because
// its checkpoint snapshot created the dir before the second node ran.
func runDirGraph(probe *runDirProbe) (*Graph, *HandlerRegistry) {
	g := NewGraph("rundir686")
	g.AddNode(&Node{ID: "First", Shape: "box", Attrs: map[string]string{"type": "probe"}})
	g.AddNode(&Node{ID: "Second", Shape: "box", Attrs: map[string]string{"type": "probe"}})
	g.AddNode(&Node{ID: "End", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "First", To: "Second"})
	g.AddEdge(&Edge{From: "Second", To: "End"})
	g.StartNode = "First"

	reg := newTestRegistry()
	reg.Register(&testHandler{name: "probe", executeFn: probe.execute})
	return g, reg
}

func TestRunDirExistsBeforeFirstNode_Fresh(t *testing.T) {
	probe := newRunDirProbe()
	g, reg := runDirGraph(probe)
	// The artifact base itself does not exist yet either.
	artifactDir := filepath.Join(t.TempDir(), ".tracker", "runs")

	res, err := NewEngine(g, reg, WithArtifactDir(artifactDir)).Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := filepath.Join(artifactDir, res.RunID)
	if probe.dirs["First"] != want {
		t.Fatalf("First saw artifact dir %q, want %q", probe.dirs["First"], want)
	}
	if !probe.exists["First"] {
		t.Fatalf("run dir %s did not exist when the first node ran (issue #686)", want)
	}
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(want)
	if err != nil {
		t.Fatalf("stat run dir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("run dir mode = %o, want 0700", perm)
	}
}

func TestRunDirExistsBeforeFirstNode_Resume(t *testing.T) {
	cases := []struct {
		name      string
		completed []string
		current   string
		probeNode string
	}{
		{name: "from_start", completed: []string{}, probeNode: "First"},
		{name: "mid_run", completed: []string{"First"}, current: "Second", probeNode: "Second"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probe := newRunDirProbe()
			g, reg := runDirGraph(probe)
			artifactDir := filepath.Join(t.TempDir(), ".tracker", "runs")
			cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
			cp := &Checkpoint{
				RunID:          "r686",
				CurrentNode:    tc.current,
				CompletedNodes: tc.completed,
				RetryCounts:    map[string]int{},
				Context:        map[string]string{},
			}
			if err := SaveCheckpoint(cp, cpPath); err != nil {
				t.Fatalf("save checkpoint: %v", err)
			}

			_, err := NewEngine(g, reg, WithArtifactDir(artifactDir), WithCheckpointPath(cpPath)).Run(context.Background())
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			want := filepath.Join(artifactDir, "r686")
			if probe.dirs[tc.probeNode] != want {
				t.Fatalf("%s saw artifact dir %q, want %q", tc.probeNode, probe.dirs[tc.probeNode], want)
			}
			if !probe.exists[tc.probeNode] {
				t.Fatalf("resumed run dir %s did not exist when the resume entry node %s ran (issue #686)", want, tc.probeNode)
			}
			if tc.probeNode != "First" {
				if _, ran := probe.dirs["First"]; ran {
					t.Errorf("completed node First re-ran on resume; the probe is not the resume entry")
				}
			}
		})
	}
}

func TestRunDirExistsBeforeFirstNode_Inherited(t *testing.T) {
	probe := newRunDirProbe()
	g, reg := runDirGraph(probe)
	runDir := filepath.Join(t.TempDir(), ".tracker", "runs", "p686")

	if _, err := NewEngine(g, reg, WithInheritedRunIdentity("p686", runDir)).Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if probe.dirs["First"] != runDir {
		t.Fatalf("First saw artifact dir %q, want inherited %q", probe.dirs["First"], runDir)
	}
	if !probe.exists["First"] {
		t.Fatalf("inherited run dir %s did not exist when the child's first node ran (issue #686)", runDir)
	}
}

func TestRunDirPreExistingContentsKept(t *testing.T) {
	probe := newRunDirProbe()
	g, reg := runDirGraph(probe)
	artifactDir := t.TempDir()
	runDir := filepath.Join(artifactDir, "r686")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(runDir, "keep.txt")
	if err := os.WriteFile(keep, []byte("prior"), 0o600); err != nil {
		t.Fatal(err)
	}
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	if err := SaveCheckpoint(&Checkpoint{RunID: "r686", RetryCounts: map[string]int{}, Context: map[string]string{}}, cpPath); err != nil {
		t.Fatal(err)
	}

	if _, err := NewEngine(g, reg, WithArtifactDir(artifactDir), WithCheckpointPath(cpPath)).Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, err := os.ReadFile(keep)
	if err != nil || string(got) != "prior" {
		t.Fatalf("pre-existing run-dir file lost or changed: %q, %v", got, err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(runDir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o755 {
		t.Errorf("pre-existing run dir mode changed to %o, want it left at 0755", perm)
	}
}

func TestNoRunDirWithoutArtifactDir(t *testing.T) {
	probe := newRunDirProbe()
	g, reg := runDirGraph(probe)
	cwd := t.TempDir()
	t.Chdir(cwd)

	if _, err := NewEngine(g, reg).Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if d, ran := probe.dirs["First"]; !ran || d != "" {
		t.Fatalf("bare engine: First ran=%v artifact dir=%q, want ran with empty dir", ran, d)
	}
	entries, err := os.ReadDir(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("bare engine created %d entries under cwd (first %q), want none", len(entries), entries[0].Name())
	}
}

func TestRunDirCreateFailureFailsRunBeforeAnyNode(t *testing.T) {
	probe := newRunDirProbe()
	g, reg := runDirGraph(probe)
	base := t.TempDir()
	blocker := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var started int
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		if evt.Type == EventPipelineStarted {
			started++
		}
	})

	res, err := NewEngine(g, reg, WithArtifactDir(filepath.Join(blocker, "runs")), WithPipelineEventHandler(handler)).Run(context.Background())
	if err == nil {
		t.Fatalf("run succeeded (status %v) with an uncreatable run dir; want a start-time error", res.Status)
	}
	if !strings.Contains(err.Error(), "create run artifact dir") {
		t.Errorf("error = %v, want it to name the run artifact dir", err)
	}
	if len(probe.dirs) != 0 {
		t.Errorf("nodes ran despite the run-dir failure: %v", probe.dirs)
	}
	if started != 0 {
		t.Errorf("EventPipelineStarted emitted %d times; the run should fail before starting", started)
	}
}
