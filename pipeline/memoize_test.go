// ABOUTME: Tests for opt-in content-hash node-output memoization (#421).
// ABOUTME: Covers replay-on-restart, input-change invalidation, checkpoint round-trip, and default-off.
package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// memoLoopGraph builds: s -> work -> check --(fail)--> work ; check --(success)--> end.
// `work` is the candidate node; `check` fails on its first attempt to force one
// loop restart that clears + re-enters `work`, then succeeds.
func memoLoopGraph(name string, workMemoize bool) *Graph {
	g := NewGraph(name)
	g.Attrs["max_restarts"] = "3"
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	work := &Node{ID: "work", Shape: "box", Label: "Work"}
	if workMemoize {
		work.Attrs = map[string]string{"memoize": "true"}
	}
	g.AddNode(work)
	g.AddNode(&Node{ID: "check", Shape: "diamond", Label: "Check"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "work"})
	g.AddEdge(&Edge{From: "work", To: "check"})
	g.AddEdge(&Edge{From: "check", To: "end", Condition: "outcome=success"})
	g.AddEdge(&Edge{From: "check", To: "work", Condition: "outcome=fail"})
	return g
}

// failOnceCheckHandler returns a conditional handler that fails on its first
// call and succeeds thereafter, driving exactly one restart.
func failOnceCheckHandler(mu *sync.Mutex, attempts *int) *testHandler {
	return &testHandler{
		name: "conditional",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			mu.Lock()
			*attempts++
			a := *attempts
			mu.Unlock()
			if a == 1 {
				return Outcome{Status: string(OutcomeFail), ContextUpdates: map[string]string{"outcome": "fail"}}, nil
			}
			return Outcome{Status: string(OutcomeSuccess), ContextUpdates: map[string]string{"outcome": "success"}}, nil
		},
	}
}

// AC1: a memoize:true node re-entered via a restart edge with identical hashed
// inputs replays its prior outcome WITHOUT invoking the handler.
func TestMemoReplaySkipsHandlerOnRestart(t *testing.T) {
	g := memoLoopGraph("memo_ac1", true)

	reg := newTestRegistry()
	var mu sync.Mutex
	workCalls := 0
	checkAttempts := 0

	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			if node.ID == "work" {
				mu.Lock()
				workCalls++
				mu.Unlock()
				return Outcome{Status: string(OutcomeSuccess), ContextUpdates: map[string]string{"work_out": "v1"}}, nil
			}
			return Outcome{Status: string(OutcomeSuccess)}, nil
		},
	})
	reg.Register(failOnceCheckHandler(&mu, &checkAttempts))

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Fatalf("expected success, got %q", result.Status)
	}

	mu.Lock()
	defer mu.Unlock()
	// `work` is entered twice (initial + after restart) but the second entry
	// must be a memo replay → handler invoked exactly once.
	if workCalls != 1 {
		t.Errorf("AC1: expected work handler called exactly 1 time across 2 entries, got %d", workCalls)
	}
	if checkAttempts != 2 {
		t.Errorf("expected check to run twice, got %d", checkAttempts)
	}
	// The replayed ContextUpdates must be re-applied so downstream sees work_out.
	if v, ok := result.Context["work_out"]; !ok || v != "v1" {
		t.Errorf("AC1: expected replayed work_out=v1 in context, got %q (ok=%v)", v, ok)
	}
}

// AC2(i): changing a bare ctx value the node hashes invalidates the memo and re-runs.
func TestMemoInvalidatesOnChangedCtxInput(t *testing.T) {
	g := memoLoopGraph("memo_ac2_ctx", true)

	reg := newTestRegistry()
	var mu sync.Mutex
	workCalls := 0
	checkAttempts := 0

	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			if node.ID == "work" {
				mu.Lock()
				workCalls++
				mu.Unlock()
				return Outcome{Status: string(OutcomeSuccess)}, nil
			}
			return Outcome{Status: string(OutcomeSuccess)}, nil
		},
	})
	// check fails first time AND mutates a bare ctx key so work's hashed input
	// changes between its two entries → memo miss → handler re-runs.
	reg.Register(&testHandler{
		name: "conditional",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			mu.Lock()
			checkAttempts++
			a := checkAttempts
			mu.Unlock()
			if a == 1 {
				return Outcome{Status: string(OutcomeFail), ContextUpdates: map[string]string{"outcome": "fail", "drift": "changed"}}, nil
			}
			return Outcome{Status: string(OutcomeSuccess), ContextUpdates: map[string]string{"outcome": "success"}}, nil
		},
	})

	engine := NewEngine(g, reg)
	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if workCalls != 2 {
		t.Errorf("AC2(ctx): expected work handler to re-run (2 calls) after ctx drift, got %d", workCalls)
	}
}

// AC2(ii): changing an interpolated prompt (different resolved attr) invalidates.
func TestMemoInvalidatesOnChangedPrompt(t *testing.T) {
	g := NewGraph("memo_ac2_prompt")
	g.Attrs["max_restarts"] = "3"
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	// work's prompt interpolates a ctx var that changes between entries.
	g.AddNode(&Node{ID: "work", Shape: "box", Label: "Work", Attrs: map[string]string{
		"memoize": "true",
		"prompt":  "do ${ctx.knob}",
	}})
	g.AddNode(&Node{ID: "check", Shape: "diamond", Label: "Check"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "work"})
	g.AddEdge(&Edge{From: "work", To: "check"})
	g.AddEdge(&Edge{From: "check", To: "end", Condition: "outcome=success"})
	g.AddEdge(&Edge{From: "check", To: "work", Condition: "outcome=fail"})

	reg := newTestRegistry()
	var mu sync.Mutex
	workCalls := 0
	checkAttempts := 0
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			if node.ID == "work" {
				mu.Lock()
				workCalls++
				mu.Unlock()
			}
			return Outcome{Status: string(OutcomeSuccess)}, nil
		},
	})
	reg.Register(&testHandler{
		name: "conditional",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			mu.Lock()
			checkAttempts++
			a := checkAttempts
			mu.Unlock()
			if a == 1 {
				return Outcome{Status: string(OutcomeFail), ContextUpdates: map[string]string{"outcome": "fail", "knob": "two"}}, nil
			}
			return Outcome{Status: string(OutcomeSuccess), ContextUpdates: map[string]string{"outcome": "success"}}, nil
		},
	})

	engine := NewEngine(g, reg)
	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if workCalls != 2 {
		t.Errorf("AC2(prompt): expected work to re-run (2 calls) after prompt drift, got %d", workCalls)
	}
}

// AC3: the memo table survives a checkpoint serialize -> deserialize -> replay.
func TestMemoSurvivesCheckpointRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cpPath := filepath.Join(dir, "cp.json")

	// First run: linear s -> work -> end. work is memoized and succeeds once.
	g := NewGraph("memo_ac3")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "work", Shape: "box", Label: "Work", Attrs: map[string]string{"memoize": "true"}})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "work"})
	g.AddEdge(&Edge{From: "work", To: "end"})

	reg := newTestRegistry()
	var mu sync.Mutex
	calls := 0
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			if node.ID == "work" {
				mu.Lock()
				calls++
				mu.Unlock()
				return Outcome{Status: string(OutcomeSuccess), ContextUpdates: map[string]string{"work_out": "persisted"}}, nil
			}
			return Outcome{Status: string(OutcomeSuccess)}, nil
		},
	})

	engine := NewEngine(g, reg, WithCheckpointPath(cpPath))
	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("run 1: %v", err)
	}

	// Verify the memo entry was persisted to disk.
	cp, err := LoadCheckpoint(cpPath)
	if err != nil {
		t.Fatalf("load checkpoint: %v", err)
	}
	if len(cp.MemoEntries) != 1 {
		t.Fatalf("AC3: expected 1 persisted memo entry, got %d", len(cp.MemoEntries))
	}

	// Force re-execution of work on resume by clearing it from completed, then
	// resume from a fresh engine that loads the checkpoint. The memo table must
	// replay work WITHOUT invoking the handler.
	cp.ClearCompleted("work")
	cp.CurrentNode = "work"
	if err := SaveCheckpoint(cp, cpPath); err != nil {
		t.Fatalf("save mutated checkpoint: %v", err)
	}

	mu.Lock()
	callsBeforeResume := calls
	mu.Unlock()

	engine2 := NewEngine(g, reg, WithCheckpointPath(cpPath))
	result2, err := engine2.Run(context.Background())
	if err != nil {
		t.Fatalf("run 2 (resume): %v", err)
	}
	if result2.Status != OutcomeSuccess {
		t.Fatalf("resume expected success, got %q", result2.Status)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != callsBeforeResume {
		t.Errorf("AC3: expected NO new handler calls on resume (pure memo replay), got %d new", calls-callsBeforeResume)
	}
	if v := result2.Context["work_out"]; v != "persisted" {
		t.Errorf("AC3: expected replayed work_out=persisted from deserialized memo, got %q", v)
	}
}

// AC4: off by default — a node WITHOUT the attr re-runs each entry and writes
// no memo_entries to the checkpoint.
func TestMemoOffByDefault(t *testing.T) {
	dir := t.TempDir()
	cpPath := filepath.Join(dir, "cp.json")
	g := memoLoopGraph("memo_ac4", false) // no memoize attr

	reg := newTestRegistry()
	var mu sync.Mutex
	workCalls := 0
	checkAttempts := 0
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			if node.ID == "work" {
				mu.Lock()
				workCalls++
				mu.Unlock()
			}
			return Outcome{Status: string(OutcomeSuccess)}, nil
		},
	})
	reg.Register(failOnceCheckHandler(&mu, &checkAttempts))

	engine := NewEngine(g, reg, WithCheckpointPath(cpPath))
	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	mu.Lock()
	if workCalls != 2 {
		t.Errorf("AC4: expected work to run each entry (2 calls) with no memoize, got %d", workCalls)
	}
	mu.Unlock()

	// The on-disk checkpoint must have no memo_entries key (omitempty proves
	// zero serialization footprint when the feature is off).
	data, err := os.ReadFile(cpPath)
	if err != nil {
		t.Fatalf("read checkpoint: %v", err)
	}
	if strings.Contains(string(data), "memo_entries") {
		t.Errorf("AC4: checkpoint must not contain memo_entries when feature is off:\n%s", data)
	}
}

// Unit: computeMemoKey determinism, sensitivity, anti-collision, and disabled.
func TestComputeMemoKey(t *testing.T) {
	g := NewGraph("memo_unit")
	e := NewEngine(g, newTestRegistry())

	mkState := func(ctxVals map[string]string) *runState {
		pctx := NewPipelineContext()
		for k, v := range ctxVals {
			pctx.Set(k, v)
		}
		return &runState{pctx: pctx, cp: &Checkpoint{}}
	}

	node := &Node{ID: "work", Handler: "codergen", Attrs: map[string]string{"memoize": "true", "prompt": "abc"}}

	s := mkState(map[string]string{"a": "1"})
	k1, ok1 := e.computeMemoKey(s, node)
	if !ok1 {
		t.Fatal("expected ok=true for memoize node")
	}
	// Identical inputs → identical key.
	k1b, _ := e.computeMemoKey(mkState(map[string]string{"a": "1"}), node)
	if k1 != k1b {
		t.Errorf("expected identical key for identical inputs, got %q vs %q", k1, k1b)
	}
	// Changed ctx → different key.
	k2, _ := e.computeMemoKey(mkState(map[string]string{"a": "2"}), node)
	if k1 == k2 {
		t.Error("expected different key when ctx value changes")
	}
	// Changed attr → different key.
	node2 := &Node{ID: "work", Handler: "codergen", Attrs: map[string]string{"memoize": "true", "prompt": "xyz"}}
	k3, _ := e.computeMemoKey(mkState(map[string]string{"a": "1"}), node2)
	if k1 == k3 {
		t.Error("expected different key when attr changes")
	}
	// Disabled → ok=false.
	plain := &Node{ID: "work", Handler: "codergen", Attrs: map[string]string{"prompt": "abc"}}
	if _, ok := e.computeMemoKey(mkState(nil), plain); ok {
		t.Error("expected ok=false when memoize is not set")
	}
	// Length-prefix anti-collision: ctx {"a":"bc"} vs {"ab":"c"} must differ.
	ka, _ := e.computeMemoKey(mkState(map[string]string{"a": "bc"}), node)
	kb, _ := e.computeMemoKey(mkState(map[string]string{"ab": "c"}), node)
	if ka == kb {
		t.Error("length-prefix anti-collision failed: distinct ctx maps hashed equal")
	}
	// Routing-scratch keys are excluded — setting them must not change the key.
	withScratch := mkState(map[string]string{"a": "1", ContextKeyOutcome: "success", ContextKeyPreferredLabel: "x"})
	ks, _ := e.computeMemoKey(withScratch, node)
	if ks != k1 {
		t.Error("routing-scratch keys must be excluded from the memo key")
	}

	// #425 review (Codex P1): a bare key this node self-aliased on a prior pass
	// is skipped ONLY while unchanged. An UNCHANGED self-output (bare == scoped)
	// must not affect the key; an OVERWRITTEN one (bare != scoped) is a genuine
	// changed input and MUST change the key, else replay is stale.
	unchangedSelf := mkState(map[string]string{"a": "1", "selfk": "out", "node.work.selfk": "out"})
	kSelf, _ := e.computeMemoKey(unchangedSelf, node)
	if kSelf != k1 {
		t.Error("unchanged self-output bare key must be excluded from the memo key")
	}
	overwrittenSelf := mkState(map[string]string{"a": "1", "selfk": "REWRITTEN", "node.work.selfk": "out"})
	kOver, _ := e.computeMemoKey(overwrittenSelf, node)
	if kOver == kSelf {
		t.Error("overwritten self-output (bare != scoped) must change the memo key, not replay stale")
	}
}

// Verify MemoEntry round-trips through JSON.
func TestMemoEntryJSONRoundTrip(t *testing.T) {
	cp := &Checkpoint{RunID: "r"}
	cp.PutMemo("k", &Outcome{Status: string(OutcomeSuccess), ContextUpdates: map[string]string{"x": "y"}})
	data, err := json.Marshal(cp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var loaded Checkpoint
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	rec, ok := loaded.GetMemo("k")
	if !ok {
		t.Fatal("expected memo entry after round-trip")
	}
	if rec.Status != string(OutcomeSuccess) || rec.ContextUpdates["x"] != "y" {
		t.Errorf("memo entry corrupted: %+v", rec)
	}
}

// #425 review (CodeRabbit): a memoized node that selected its next edge via
// PreferredLabel / SuggestedNextNodes must replay onto the SAME path. Verify
// both routing hints survive PutMemo + a JSON checkpoint round-trip.
func TestMemoEntryPersistsRoutingHints(t *testing.T) {
	cp := &Checkpoint{RunID: "r"}
	cp.PutMemo("k", &Outcome{
		Status:             string(OutcomeSuccess),
		PreferredLabel:     "approve",
		SuggestedNextNodes: []string{"join", "fanin"},
	})
	data, err := json.Marshal(cp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var loaded Checkpoint
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	rec, ok := loaded.GetMemo("k")
	if !ok {
		t.Fatal("expected memo entry after round-trip")
	}
	if rec.PreferredLabel != "approve" {
		t.Errorf("PreferredLabel not persisted: got %q", rec.PreferredLabel)
	}
	if len(rec.SuggestedNextNodes) != 2 || rec.SuggestedNextNodes[0] != "join" || rec.SuggestedNextNodes[1] != "fanin" {
		t.Errorf("SuggestedNextNodes not persisted: got %v", rec.SuggestedNextNodes)
	}
}

// #425 review (Codex P2): TreeFingerprint must reflect actual worktree CONTENT,
// not just staged blobs. A content change to an existing tracked file that is
// neither staged nor changes its status marker must still flip the fingerprint.
func TestTreeFingerprintDetectsUnstagedTrackedContentChange(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	repo := newGitArtifactRepo(dir, "unstaged01")
	if err := repo.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	// Commit a tracked file so it lives in HEAD and the index.
	path := filepath.Join(dir, "tracked.txt")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := repo.git("add", "tracked.txt"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	if out, err := repo.git("commit", "-m", "seed"); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}

	fp1, err := repo.TreeFingerprint()
	if err != nil {
		t.Fatalf("fingerprint 1: %v", err)
	}
	// Modify the tracked file in the worktree WITHOUT staging — `ls-files -s`
	// (the old impl) would still show the committed blob and miss this.
	if err := os.WriteFile(path, []byte("CHANGED-UNSTAGED"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	fp2, err := repo.TreeFingerprint()
	if err != nil {
		t.Fatalf("fingerprint 2: %v", err)
	}
	if fp1 == fp2 {
		t.Error("expected fingerprint to change on an unstaged tracked-file content change")
	}
}

// AC2(iii): for a writable_paths agent backed by a live repo, a working-tree
// change between entries flips the tree fingerprint and thus the memo key.
func TestMemoKeyTreeFingerprintSensitivity(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	repo := newGitArtifactRepo(dir, "treerun01")
	if err := repo.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}

	g := NewGraph("memo_tree")
	e := NewEngine(g, newTestRegistry())
	node := &Node{ID: "work", Handler: "codergen", Attrs: map[string]string{
		"memoize":        "true",
		"writable_paths": "out/**",
	}}
	s := &runState{pctx: NewPipelineContext(), cp: &Checkpoint{}, gitRepo: repo}

	k1, ok := e.computeMemoKey(s, node)
	if !ok {
		t.Fatal("expected ok=true with live repo")
	}

	// Mutate the working tree.
	if err := os.WriteFile(filepath.Join(dir, "newfile.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	k2, ok := e.computeMemoKey(s, node)
	if !ok {
		t.Fatal("expected ok=true after tree change")
	}
	if k1 == k2 {
		t.Error("AC2(tree): expected memo key to change after working-tree mutation")
	}
}

// A tree-fingerprint error yields a cache miss (ok=false), not a stale replay.
func TestMemoKeyTreeFingerprintErrorIsMiss(t *testing.T) {
	g := NewGraph("memo_tree_err")
	e := NewEngine(g, newTestRegistry())
	node := &Node{ID: "work", Handler: "codergen", Attrs: map[string]string{
		"memoize":        "true",
		"writable_paths": "out/**",
	}}
	// gitRepo points at a non-existent dir → git status fails → ok=false.
	badRepo := newGitArtifactRepo(filepath.Join(t.TempDir(), "does-not-exist"), "bad")
	s := &runState{pctx: NewPipelineContext(), cp: &Checkpoint{}, gitRepo: badRepo}

	if _, ok := e.computeMemoKey(s, node); ok {
		t.Error("expected ok=false (cache miss) when tree fingerprint errors")
	}
}

// BLOCKER 1 (run-b65fb64 shape): work writes bare last_response on entry 1
// (so node.work.last_response gets aliased — the old self-output exclusion would
// drop last_response from the key). The intervening check node OVERWRITES bare
// last_response with a NEW critique before work re-enters. Because last_response
// is a genuine InjectPipelineContext prompt INPUT, the memo key MUST change and
// work MUST re-run — handler called TWICE, not once.
func TestMemoInvalidatesOnChangedInjectedLastResponse(t *testing.T) {
	g := memoLoopGraph("memo_b1", true)

	reg := newTestRegistry()
	var mu sync.Mutex
	workCalls := 0
	checkAttempts := 0

	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			if node.ID == "work" {
				mu.Lock()
				workCalls++
				mu.Unlock()
				// work writes bare last_response — on scope it aliases to
				// node.work.last_response, the trigger for the old self-output bug.
				return Outcome{Status: string(OutcomeSuccess), ContextUpdates: map[string]string{
					ContextKeyLastResponse: "work output",
				}}, nil
			}
			return Outcome{Status: string(OutcomeSuccess)}, nil
		},
	})
	// check fails first time and overwrites bare last_response with a NEW critique,
	// genuinely changing work's resolved prompt input on re-entry.
	reg.Register(&testHandler{
		name: "conditional",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			mu.Lock()
			checkAttempts++
			a := checkAttempts
			mu.Unlock()
			if a == 1 {
				return Outcome{Status: string(OutcomeFail), ContextUpdates: map[string]string{
					"outcome":              "fail",
					ContextKeyLastResponse: "review critique: fix X",
				}}, nil
			}
			return Outcome{Status: string(OutcomeSuccess), ContextUpdates: map[string]string{"outcome": "success"}}, nil
		},
	})

	engine := NewEngine(g, reg)
	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if workCalls != 2 {
		t.Errorf("B1: expected work to re-run (2 calls) after intervening node changed bare last_response, got %d", workCalls)
	}
}

// BLOCKER 2: a memoize:true node declaring writable_paths run WITHOUT a git repo
// (the default tracker run / library Run config — WithGitArtifacts has no
// production callers). With no tree fingerprint available there is no way to
// prove the working tree is unchanged, so computeMemoKey MUST return ok=false
// (hard miss) rather than replay a side-effecting node on a stale tree.
func TestMemoKeyWritablePathsRequiresTreeFingerprint(t *testing.T) {
	g := NewGraph("memo_b2")
	e := NewEngine(g, newTestRegistry())
	node := &Node{ID: "work", Handler: "codergen", Attrs: map[string]string{
		"memoize":        "true",
		"writable_paths": "out/**",
	}}
	// Default config: gitRepo is nil (WithGitArtifacts not wired into production).
	s := &runState{pctx: NewPipelineContext(), cp: &Checkpoint{}}

	if _, ok := e.computeMemoKey(s, node); ok {
		t.Error("B2: expected ok=false (cache miss) for a writable_paths node with no tree fingerprint")
	}
}

// PutMemo must deep-copy ContextUpdates so later mutation cannot corrupt it.
func TestPutMemoDeepCopiesContextUpdates(t *testing.T) {
	cp := &Checkpoint{}
	cu := map[string]string{"x": "v1"}
	cp.PutMemo("k", &Outcome{Status: string(OutcomeSuccess), ContextUpdates: cu})
	cu["x"] = "mutated"
	rec, _ := cp.GetMemo("k")
	if rec.ContextUpdates["x"] != "v1" {
		t.Errorf("expected stored record to be insulated from caller mutation, got %q", rec.ContextUpdates["x"])
	}
}

// memoOutcome must deep-copy the reference-typed fields so mutating the replay
// Outcome cannot reach back into the checkpoint memo record (#425 review).
func TestMemoOutcomeIsolatesRecordFromMutation(t *testing.T) {
	rec := MemoEntry{
		Status:             string(OutcomeSuccess),
		ContextUpdates:     map[string]string{"x": "v1"},
		PreferredLabel:     "approve",
		SuggestedNextNodes: []string{"join"},
	}
	out := memoOutcome(rec)
	// Mutate every reference field on the replay Outcome.
	out.ContextUpdates["x"] = "mutated"
	out.ContextUpdates["y"] = "added"
	out.SuggestedNextNodes[0] = "elsewhere"

	if rec.ContextUpdates["x"] != "v1" {
		t.Errorf("record ContextUpdates corrupted: x=%q, want v1", rec.ContextUpdates["x"])
	}
	if _, ok := rec.ContextUpdates["y"]; ok {
		t.Error("record ContextUpdates gained a key from replay mutation")
	}
	if rec.SuggestedNextNodes[0] != "join" {
		t.Errorf("record SuggestedNextNodes corrupted: [0]=%q, want join", rec.SuggestedNextNodes[0])
	}
}
