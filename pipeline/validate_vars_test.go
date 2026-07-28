// ABOUTME: Tests for submit-time variable-availability validation (#505).
// ABOUTME: Covers the misspelled-key rejection plus every fail-open case that must NOT be flagged.
package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// varGraph builds a small graph for the availability checks. Nodes are given as
// id -> attrs; edges as "from>to" or "from>to|condition".
func varGraph(t *testing.T, nodes map[string]map[string]string, edges ...string) *Graph {
	t.Helper()
	g := NewGraph("t")
	for id, attrs := range nodes {
		handler := attrs["type"]
		if handler == "" {
			handler = "codergen"
		}
		copied := map[string]string{}
		for k, v := range attrs {
			if k != "type" {
				copied[k] = v
			}
		}
		g.AddNode(&Node{ID: id, Handler: handler, Attrs: copied})
		g.NodeOrder = append(g.NodeOrder, id)
	}
	for _, spec := range edges {
		parts := strings.SplitN(spec, "|", 2)
		ends := strings.SplitN(parts[0], ">", 2)
		e := &Edge{From: ends[0], To: ends[1]}
		if len(parts) == 2 {
			e.Condition = parts[1]
		}
		g.AddEdge(e)
	}
	return g
}

func joined(msgs []string) string { return strings.Join(msgs, "\n") }

func TestValidateVariableAvailability_MisspelledConditionKeyIsError(t *testing.T) {
	g := varGraph(t,
		map[string]map[string]string{
			"Plan":  {"writes": "milestone_id"},
			"Build": {},
			"Fix":   {},
		},
		"Plan>Build|ctx.milestone_id = ok",
		"Build>Fix|ctx.mispelled_key = success",
	)

	errs, _ := ValidateVariableAvailability(g)
	if len(errs) != 1 {
		t.Fatalf("want exactly 1 error, got %d: %v", len(errs), errs)
	}
	msg := errs[0]
	if !strings.Contains(msg, "Build") {
		t.Errorf("error must name the offending node: %q", msg)
	}
	if !strings.Contains(msg, "mispelled_key") {
		t.Errorf("error must name the offending key: %q", msg)
	}
}

func TestValidateVariableAvailability_EngineProvidedKeysNotFlagged(t *testing.T) {
	engineKeys := []string{
		"outcome", "preferred_label", "last_response", "human_response",
		"tool_stdout", "tool_stderr", "tool_marker", "tool_marker_error",
		"tool_route", "suggested_next_nodes", "turn_limit_msg",
		"turn_breach_class", "node_cost_exceeded", "node_no_progress",
		"episode_summary", "episode_summaries", "interview_questions",
		"interview_answers", "last_cost", "last_turns", "writes_error",
		"writes_warning", "completed_summary", "parallel.results",
		"fan_in.policy_detail", "graph.goal", "graph.anything",
		"params.model", "response.Plan", "steer.hint",
		"stack.child.status", "summary.Plan", "internal.loop_restart_count",
	}
	var prompt strings.Builder
	for _, k := range engineKeys {
		prompt.WriteString("${ctx." + k + "} ")
	}

	for _, k := range engineKeys {
		g := varGraph(t,
			map[string]map[string]string{
				"Plan":  {"prompt": prompt.String()},
				"Build": {},
			},
			"Plan>Build|ctx."+k+" = whatever",
		)
		errs, warns := ValidateVariableAvailability(g)
		if len(errs) != 0 || len(warns) != 0 {
			t.Errorf("engine key %q flagged: errs=%v warns=%v", k, errs, warns)
		}
	}
}

func TestValidateVariableAvailability_LoopWrittenKeyNotFlagged(t *testing.T) {
	// Check writes fix_verdict and loops back to Fix, which reads it. A
	// topological check would flag Fix; plain reachability must not.
	g := varGraph(t,
		map[string]map[string]string{
			"Fix":   {"prompt": "verdict was ${ctx.fix_verdict}", "reads": "fix_verdict"},
			"Check": {"writes": "fix_verdict"},
			"Done":  {"type": "exit"},
		},
		"Fix>Check",
		"Check>Fix|ctx.fix_verdict = retry",
		"Check>Done|ctx.fix_verdict = ok",
	)

	errs, warns := ValidateVariableAvailability(g)
	if len(errs) != 0 || len(warns) != 0 {
		t.Fatalf("loop-written key flagged: errs=%v warns=%v", errs, warns)
	}
}

func TestValidateVariableAvailability_SelfWrittenKeyNotFlagged(t *testing.T) {
	g := varGraph(t,
		map[string]map[string]string{
			"Loop": {"writes": "iteration", "prompt": "iteration ${ctx.iteration}"},
			"Done": {"type": "exit"},
		},
		"Loop>Done",
	)
	errs, warns := ValidateVariableAvailability(g)
	if len(errs) != 0 || len(warns) != 0 {
		t.Fatalf("self-written key flagged: errs=%v warns=%v", errs, warns)
	}
}

func TestValidateVariableAvailability_UnreachableProducerIsWarning(t *testing.T) {
	// Late writes side_note; Early references it but Late cannot reach Early.
	g := varGraph(t,
		map[string]map[string]string{
			"Early": {},
			"Late":  {"writes": "side_note"},
			"Done":  {"type": "exit"},
		},
		"Early>Late|ctx.side_note = ok",
		"Late>Done",
	)

	errs, warns := ValidateVariableAvailability(g)
	if len(errs) != 0 {
		t.Fatalf("unreachable producer must not be an error: %v", errs)
	}
	if len(warns) != 1 || !strings.Contains(joined(warns), "side_note") {
		t.Fatalf("want one warning naming side_note, got %v", warns)
	}
	if !strings.Contains(joined(warns), "Early") {
		t.Errorf("warning must name the referencing node: %v", warns)
	}
}

func TestValidateVariableAvailability_UnknownReadsKeyIsError(t *testing.T) {
	g := varGraph(t,
		map[string]map[string]string{
			"Plan":  {},
			"Build": {"reads": "ctx.nonexistent_key"},
		},
		"Plan>Build",
	)
	errs, _ := ValidateVariableAvailability(g)
	if len(errs) != 1 || !strings.Contains(errs[0], "nonexistent_key") || !strings.Contains(errs[0], "Build") {
		t.Fatalf("want one error naming Build and nonexistent_key, got %v", errs)
	}
}

func TestValidateVariableAvailability_UnknownInterpolationIsWarningOnly(t *testing.T) {
	g := varGraph(t,
		map[string]map[string]string{
			"Plan":  {},
			"Build": {"prompt": "use ${ctx.who_knows}"},
		},
		"Plan>Build",
	)
	errs, warns := ValidateVariableAvailability(g)
	if len(errs) != 0 {
		t.Fatalf("interpolation must never be an error: %v", errs)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "who_knows") {
		t.Fatalf("want one warning naming who_knows, got %v", warns)
	}
}

func TestValidateVariableAvailability_BareConditionOperandIsWarningOnly(t *testing.T) {
	g := varGraph(t,
		map[string]map[string]string{
			"Plan":  {},
			"Build": {},
		},
		"Plan>Build|mystery = 1",
	)
	errs, warns := ValidateVariableAvailability(g)
	if len(errs) != 0 {
		t.Fatalf("bare operand must not be an error: %v", errs)
	}
	if len(warns) != 1 {
		t.Fatalf("want one warning, got %v", warns)
	}
}

func TestValidateVariableAvailability_OpaqueNodesSuppressFindings(t *testing.T) {
	for _, handler := range []string{"subgraph", "stack.manager_loop"} {
		g := varGraph(t,
			map[string]map[string]string{
				"Opaque": {"type": handler},
				"Build":  {"reads": "child_key", "prompt": "${ctx.child_key}"},
			},
			"Opaque>Build|ctx.child_key = ok",
		)
		errs, warns := ValidateVariableAvailability(g)
		if len(errs) != 0 || len(warns) != 0 {
			t.Errorf("%s must suppress findings downstream: errs=%v warns=%v", handler, errs, warns)
		}
	}
}

func TestValidateVariableAvailability_AnswersKeyIsAProducer(t *testing.T) {
	g := varGraph(t,
		map[string]map[string]string{
			"Ask":   {"type": "wait.human", "mode": "interview", "answers_key": "scope_answers"},
			"Build": {"prompt": "scope: ${ctx.scope_answers}"},
		},
		"Ask>Build",
	)
	errs, warns := ValidateVariableAvailability(g)
	if len(errs) != 0 || len(warns) != 0 {
		t.Fatalf("answers_key producer not recognized: errs=%v warns=%v", errs, warns)
	}
}

func TestValidateVariableAvailability_ScopedRefUnknownNodeIsWarning(t *testing.T) {
	g := varGraph(t,
		map[string]map[string]string{
			"Plan":  {},
			"Build": {"prompt": "${ctx.node.Typo.last_response} ${ctx.node.Plan.last_response}"},
		},
		"Plan>Build",
	)
	errs, warns := ValidateVariableAvailability(g)
	if len(errs) != 0 {
		t.Fatalf("scoped ref must not be an error: %v", errs)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "Typo") {
		t.Fatalf("want one warning naming Typo, got %v", warns)
	}
}

func TestValidateVariableAvailability_OneFindingPerNodeKeyPair(t *testing.T) {
	// The same missing key is declared in reads: (error) and interpolated twice
	// (warning). One error, no warning.
	g := varGraph(t,
		map[string]map[string]string{
			"Plan":  {},
			"Build": {"reads": "ghost", "prompt": "${ctx.ghost}", "system": "${ctx.ghost}"},
		},
		"Plan>Build",
	)
	errs, warns := ValidateVariableAvailability(g)
	if len(errs) != 1 {
		t.Fatalf("want exactly 1 error, got %v", errs)
	}
	if len(warns) != 0 {
		t.Fatalf("the softer duplicates must be absorbed, got %v", warns)
	}
}

func TestValidateVariableAvailability_NilGraph(t *testing.T) {
	errs, warns := ValidateVariableAvailability(nil)
	if errs != nil || warns != nil {
		t.Fatalf("nil graph must be silent, got %v / %v", errs, warns)
	}
}

func TestValidateSemantic_SurfacesUnavailableVariable(t *testing.T) {
	g := varGraph(t,
		map[string]map[string]string{
			"Plan":  {},
			"Build": {},
		},
		"Plan>Build|ctx.typo_here = success",
	)
	reg := NewHandlerRegistry()
	reg.Register(&semanticStubHandler{name: "codergen"})
	err, _ := ValidateSemantic(g, reg)
	if err == nil || !strings.Contains(err.Error(), "typo_here") {
		t.Fatalf("ValidateSemantic must surface the unavailable variable, got %v", err)
	}
}

// TestValidateVariableAvailability_ExamplesClean is the false-positive guard:
// every shipped example must produce zero errors AND zero warnings. A finding
// here is a bug in the rule, not in the example.
func TestValidateVariableAvailability_ExamplesClean(t *testing.T) {
	root := filepath.Join("..", "examples")
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".dip") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk examples: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no example .dip files found")
	}

	for _, path := range files {
		source, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		g, _, loadErr := LoadDippinWorkflow(string(source), filepath.Base(path))
		if loadErr != nil {
			t.Logf("skipping %s (does not load standalone): %v", path, loadErr)
			continue
		}
		errs, warns := ValidateVariableAvailability(g)
		if len(errs) > 0 || len(warns) > 0 {
			t.Errorf("%s: errors=%v warnings=%v", path, errs, warns)
		}
	}
}
