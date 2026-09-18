// ABOUTME: Issue #646 item 5 regression guards on the REAL build_product_with_superspec.dip graph.
// ABOUTME: Engine sims prove the fail-closed routing (AbortRun / MergeConflict, never accept) and sidecar shape.
package pipeline

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func superspecSim() *sim646 {
	return &sim646{
		script:    map[string]func(n int) Outcome{},
		gates:     map[string]string{"ApprovePlan": "approve"},
		terminals: map[string]bool{"AbortRun": true},
	}
}

// TestSuperspec646HappyPath: every phase merges and gates green, the scaffold
// is committed before the first worktree, the run ships; no gate other than
// ApprovePlan is asked.
func TestSuperspec646HappyPath(t *testing.T) {
	g := loadBuildProductSuperspec(t)
	s := superspecSim()
	res, err := s.run(t, g)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Status != OutcomeSuccess {
		t.Fatalf("status = %s, want success", res.Status)
	}
	for _, id := range []string{"CommitScaffold", "SetupPhase1Worktrees", "MergePhase1", "GatePhase1", "MergePhase5", "FinalGates", "TraceabilityAudit", "Cleanup", "FinalCommit"} {
		if !s.visited(id) {
			t.Errorf("%s not visited", id)
		}
	}
	for _, id := range []string{"AbortRun", "MergeConflict", "RetryMerge", "EscalateToHuman"} {
		if s.visited(id) {
			t.Errorf("%s visited on the happy path", id)
		}
	}
}

// TestSuperspec646MergeConflictAbandons (5c): a failing phase merge reaches
// the MergeConflict gate; "abandon" (the unattended default) ends at the
// AbortRun terminal — never GatePhaseN, never the accept gate.
func TestSuperspec646MergeConflictAbandons(t *testing.T) {
	g := loadBuildProductSuperspec(t)
	s := superspecSim()
	s.script["MergePhase2"] = func(int) Outcome { return bpFail("MERGE CONFLICT in stream-c") }
	s.gates["MergeConflict"] = "abandon"
	res, _ := s.run(t, g)
	if res != nil && res.Status == OutcomeSuccess {
		t.Fatal("run succeeded after a merge conflict")
	}
	if !s.visited("MergeConflict") || !s.visited("AbortRun") {
		t.Errorf("expected MergeConflict → AbortRun, seen=%v", s.seen)
	}
	for _, id := range []string{"GatePhase2", "EscalateToHuman", "Cleanup", "FinalCommit"} {
		if s.visited(id) {
			t.Errorf("%s visited after a merge conflict", id)
		}
	}
}

// TestSuperspec646MergeConflictRetry (5c): "retry" routes through RetryMerge
// back to the SAME phase's merge (exact marker), which then succeeds and the
// run continues to that phase's gate.
func TestSuperspec646MergeConflictRetry(t *testing.T) {
	g := loadBuildProductSuperspec(t)
	s := superspecSim()
	s.script["MergePhase4"] = func(n int) Outcome {
		if n == 0 {
			return bpFail("MERGE CONFLICT in stream-f")
		}
		return bpOK("phase4-merged")
	}
	s.script["RetryMerge"] = func(int) Outcome { return bpOK("retrying the phase 4 merge\nretry-merge-phase4") }
	s.gates["MergeConflict"] = "retry"
	res, err := s.run(t, g)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Status != OutcomeSuccess {
		t.Fatalf("status = %s, want success after a retried merge", res.Status)
	}
	if s.seen["MergePhase4"] != 2 || !s.visited("RetryMerge") || !s.visited("GatePhase4") {
		t.Errorf("expected MergePhase4 ×2 via RetryMerge then GatePhase4, seen=%v", s.seen)
	}
	if s.visited("AbortRun") || s.visited("MergePhase1") && s.seen["MergePhase1"] != 1 {
		t.Errorf("retry re-entered the wrong merge or aborted, seen=%v", s.seen)
	}
}

// TestSuperspec646RetryMergeWithoutPhaseAborts: RetryMerge with no usable
// phase marker takes the unconditional fallback to the terminal.
func TestSuperspec646RetryMergeWithoutPhaseAborts(t *testing.T) {
	g := loadBuildProductSuperspec(t)
	s := superspecSim()
	s.script["MergePhase1"] = func(int) Outcome { return bpFail("MERGE CONFLICT") }
	s.script["RetryMerge"] = func(int) Outcome { return bpFail("ERROR: no merge phase recorded") }
	s.gates["MergeConflict"] = "retry"
	res, _ := s.run(t, g)
	if res != nil && res.Status == OutcomeSuccess {
		t.Fatal("run succeeded")
	}
	if !s.visited("AbortRun") || s.seen["MergePhase1"] != 1 {
		t.Errorf("expected AbortRun without re-entering MergePhase1, seen=%v", s.seen)
	}
}

// TestSuperspec646MechanicalFailuresAbort (5a/5e + A2): Setup, CommitScaffold,
// every SetupPhaseNWorktrees and Cleanup route their own failure to AbortRun;
// an unrouted agent failure (a stream) hits the graph on_failure = AbortRun.
// None reach EscalateToHuman's accept → Cleanup → FinalCommit path.
func TestSuperspec646MechanicalFailuresAbort(t *testing.T) {
	g := loadBuildProductSuperspec(t)
	if got := g.Attrs["fallback_target"]; got != "AbortRun" {
		t.Fatalf("graph-level on_failure/fallback_target = %q, want AbortRun", got)
	}
	for _, node := range []string{"Setup", "CommitScaffold", "SetupPhase1Worktrees", "SetupPhase2Worktrees", "SetupPhase4Worktrees", "SetupPhase5Worktrees", "Cleanup", "StreamD", "AnalyzeSpec"} {
		s := superspecSim()
		s.script[node] = func(int) Outcome { return bpFail("boom") }
		res, _ := s.run(t, g)
		if res != nil && res.Status == OutcomeSuccess {
			t.Errorf("%s failure: run succeeded", node)
		}
		if !s.visited("AbortRun") {
			t.Errorf("%s failure: AbortRun not visited, seen=%v", node, s.seen)
		}
		if s.visited("EscalateToHuman") || s.visited("FinalCommit") {
			t.Errorf("%s failure reached the accept path, seen=%v", node, s.seen)
		}
	}
	for _, from := range []string{"Setup", "CommitScaffold", "SetupPhase1Worktrees", "SetupPhase2Worktrees", "SetupPhase4Worktrees", "SetupPhase5Worktrees", "Cleanup"} {
		if edgeIndex(g, from, "AbortRun", "ctx.outcome = fail") < 0 {
			t.Errorf("%s has no `when ctx.outcome = fail` edge to AbortRun", from)
		}
	}
	for _, from := range []string{"MergePhase1", "MergePhase2", "MergePhase4", "MergePhase5"} {
		if edgeIndex(g, from, "MergeConflict", "ctx.outcome = fail") < 0 {
			t.Errorf("%s has no fail edge to MergeConflict", from)
		}
		if edgeIndex(g, from, "AbortRun", "") < 0 {
			t.Errorf("%s lacks the unconditional fallback to AbortRun", from)
		}
		if edgeIndex(g, from, "EscalateToHuman", "") >= 0 || edgeIndex(g, from, "EscalateToHuman", "ctx.outcome = fail") >= 0 {
			t.Errorf("%s still routes to EscalateToHuman", from)
		}
	}
	if edgeIndex(g, "ApprovePlan", "CommitScaffold", "") < 0 && !superspecHasLabeledEdge(g, "ApprovePlan", "CommitScaffold", "approve") {
		t.Error("ApprovePlan approve must go to CommitScaffold before any worktree")
	}
	if edgeIndex(g, "CommitScaffold", "SetupPhase1Worktrees", "") < 0 {
		t.Error("CommitScaffold must lead to SetupPhase1Worktrees")
	}
}

// TestSuperspec646RedFinalGatesUnattendedNeverShips: red FinalGates (or a
// failed TraceabilityAudit / SpecLint) reach EscalateToHuman, whose default
// is abandon — unattended, Cleanup/FinalCommit never run; the accept edge is
// an audited override.
func TestSuperspec646RedFinalGatesUnattendedNeverShips(t *testing.T) {
	g := loadBuildProductSuperspec(t)
	gate := g.Nodes["EscalateToHuman"]
	if gate.Attrs["default_choice"] != "abandon" {
		t.Fatalf("EscalateToHuman default = %q, want abandon", gate.Attrs["default_choice"])
	}
	var acceptOverride bool
	for _, e := range g.OutgoingEdges("EscalateToHuman") {
		if e.Label == "accept" && e.To == "Cleanup" && e.Override {
			acceptOverride = true
		}
	}
	// The freeform gate path takes the FIRST edge label under --auto-approve
	// (dippin's `default:` is stored as default_choice, which that path does
	// not read), so the first label must be the declared default.
	first := g.OutgoingEdges("EscalateToHuman")[0].Label
	if first != gate.Attrs["default_choice"] {
		t.Fatalf("EscalateToHuman first label = %q, default = %q — they must agree (auto-approve takes the first)", first, gate.Attrs["default_choice"])
	}
	if !acceptOverride {
		t.Error("EscalateToHuman accept -> Cleanup must be override: true")
	}
	for _, red := range []string{"FinalGates", "TraceabilityAudit", "SpecLint"} {
		s := superspecSim()
		s.script[red] = func(int) Outcome { return bpFail("red") }
		s.gates["EscalateToHuman"] = first // what --auto-approve picks
		s.run(t, g)
		if !s.visited("EscalateToHuman") {
			t.Errorf("%s red: EscalateToHuman not visited, seen=%v", red, s.seen)
		}
		if s.visited("Cleanup") || s.visited("FinalCommit") {
			t.Errorf("%s red: shipped under the unattended default, seen=%v", red, s.seen)
		}
	}
}

// TestSuperspec646PhaseSidecarsAreUniform pins the sidecars the shell fixture
// suites do not run one by one: every SetupPhaseNWorktrees.sh / MergePhaseN.sh
// is the lib one-liner with the right phase number and streams, and every
// gate sidecar drives lib/gates.sh with the right gate name (the phase-1
// suite exercises the runner itself; FinalGates has its own suite).
func TestSuperspec646PhaseSidecarsAreUniform(t *testing.T) {
	dir := filepath.Join("..", "examples", "scripts", "build_product_with_superspec")
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(b)
	}
	phases := map[string]string{"1": "stream-a stream-b", "2": "stream-c stream-e", "4": "stream-f stream-g", "5": "stream-h stream-i"}
	guard := `[ -n "${graph.workflow_dir}" ] ||`
	lib := `LIB="${graph.workflow_dir}/scripts/build_product_with_superspec/lib"`
	for n, streams := range phases {
		setup := read("SetupPhase" + n + "Worktrees.sh")
		for _, want := range []string{guard, lib, `. "$LIB/worktrees.sh"`, "setup_stream_worktrees " + n + " " + streams + "\n", "printf 'phase" + n + "-worktrees-ready'"} {
			if !strings.Contains(setup, want) {
				t.Errorf("SetupPhase%sWorktrees.sh lacks %q", n, want)
			}
		}
		merge := read("MergePhase" + n + ".sh")
		for _, want := range []string{guard, lib, `. "$LIB/traceability.sh"`, `. "$LIB/worktrees.sh"`, "merge_streams " + n + " " + streams + "\n", "printf 'phase" + n + "-merged'"} {
			if !strings.Contains(merge, want) {
				t.Errorf("MergePhase%s.sh lacks %q", n, want)
			}
		}
		if strings.Contains(merge, "git merge --abort\n") || strings.Contains(merge, "branch -D") {
			t.Errorf("MergePhase%s.sh carries inline merge/branch logic instead of lib/worktrees.sh", n)
		}
	}
	gates := map[string]string{"GatePhase1.sh": "phase1", "GatePhase2.sh": "phase2", "GatePhase4.sh": "phase4", "GateStreamD.sh": "stream-d", "FinalGates.sh": "final"}
	firstMatch := regexp.MustCompile(`(?m)^\s*(if|elif) \[ -f (go\.mod|pyproject\.toml) \]; then`)
	for file, name := range gates {
		body := read(file)
		for _, want := range []string{guard, lib, `. "$LIB/gate-integrity.sh"`, `. "$LIB/gates.sh"`, "start_gate " + name + ` "$LIB"` + "\n", "gate_verify", "finish_gate\n"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %q", file, want)
			}
		}
		if firstMatch.MatchString(body) {
			t.Errorf("%s still carries a first-match stack chain (#646 5f) — the gate must go through lib/gates.sh → verify.sh", file)
		}
	}
	if body := read("FinalGates.sh"); !strings.Contains(body, "gate_verify --final") || !strings.Contains(body, "trace_waived") {
		t.Error("FinalGates.sh must run verify.sh --final and gate NULL test_refs through the waiver file")
	}
	// The Stream prompts must point at the committed plan and the overlay
	// contract; BuildPlan must write the committed plan path.
	pdir := filepath.Join("..", "examples", "prompts", "build_product_with_superspec")
	for _, st := range []string{"A", "B", "C", "E", "F", "G", "H", "I"} {
		b, err := os.ReadFile(filepath.Join(pdir, "Stream"+st+".md"))
		if err != nil {
			t.Fatal(err)
		}
		body := string(b)
		if strings.Contains(body, ".ai/decisions/execution-plan.md") || !strings.Contains(body, "docs/execution-plan.md") {
			t.Errorf("Stream%s.md must read the COMMITTED docs/execution-plan.md", st)
		}
		if !strings.Contains(body, "docs/traceability.stream-"+strings.ToLower(st)+".yaml") || !strings.Contains(body, "NEVER edit docs/traceability.yaml") {
			t.Errorf("Stream%s.md must write its traceability OVERLAY and never the master", st)
		}
	}
	b, _ := os.ReadFile(filepath.Join(pdir, "BuildPlan.md"))
	if !strings.Contains(string(b), "execution plan to docs/execution-plan.md") || strings.Contains(string(b), ".ai/decisions/execution-plan.md") {
		t.Error("BuildPlan.md must write the plan to the committed docs/execution-plan.md")
	}
}

func superspecHasLabeledEdge(g *Graph, from, to, label string) bool {
	for _, e := range g.OutgoingEdges(from) {
		if e.To == to && e.Label == label {
			return true
		}
	}
	return false
}
