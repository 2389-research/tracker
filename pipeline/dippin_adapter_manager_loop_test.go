// ABOUTME: Tests for the ir.NodeManagerLoop adapter path introduced in dippin-lang v0.22.0.
// ABOUTME: Covers flat-attr extraction, percent-encoded steer_context round-trip, and the start/exit shape-override gotcha.
package pipeline

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/dippin-lang/ir"
)

// TestFromDippinIR_ManagerLoopFlatAttrs verifies that all six ManagerLoopConfig
// fields are flattened to the unprefixed DOT-style attrs the handler consumes.
func TestFromDippinIR_ManagerLoopFlatAttrs(t *testing.T) {
	// Exercise percent-encoding with keys/values that contain all three reserved
	// delimiter chars: ',' (pair separator), '=' (k/v separator), '%' (escape).
	steerContext := map[string]string{
		"hint":     "speed,up",      // ',' in value
		"priority": "high=critical", // '=' in value
		"tag":      "50%off",        // '%' in value
	}

	workflow := &ir.Workflow{
		Name:  "MgrLoopFlatAttrs",
		Start: "start",
		Exit:  "exit",
		Nodes: []*ir.Node{
			{ID: "start", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			{
				ID:   "mgr",
				Kind: ir.NodeManagerLoop,
				Config: ir.ManagerLoopConfig{
					SubgraphRef:    "./child.dip",
					PollInterval:   30 * time.Second,
					MaxCycles:      42,
					StopCondition:  &ir.Condition{Raw: "stack.child.cycles = 10"},
					SteerCondition: &ir.Condition{Raw: "stack.child.cycles = 5"},
					SteerContext:   steerContext,
				},
			},
			{ID: "exit", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
		},
		Edges: []*ir.Edge{
			{From: "start", To: "mgr"},
			{From: "mgr", To: "exit"},
		},
	}

	g, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}

	node, ok := g.Nodes["mgr"]
	if !ok {
		t.Fatalf("mgr node missing from graph")
	}

	// Shape and handler: kind=manager_loop → shape=house → handler=stack.manager_loop.
	if node.Shape != "house" {
		t.Errorf("mgr.Shape = %q, want %q", node.Shape, "house")
	}
	if node.Handler != "stack.manager_loop" {
		t.Errorf("mgr.Handler = %q, want %q", node.Handler, "stack.manager_loop")
	}

	// Flat scalar attrs.
	cases := []struct {
		key  string
		want string
	}{
		{"subgraph_ref", "./child.dip"},
		{"poll_interval", "30s"},
		{"max_cycles", "42"},
		{"stop_condition", "stack.child.cycles = 10"},
		{"steer_condition", "stack.child.cycles = 5"},
	}
	for _, tc := range cases {
		if got := node.Attrs[tc.key]; got != tc.want {
			t.Errorf("mgr.Attrs[%q] = %q, want %q", tc.key, got, tc.want)
		}
	}

	// steer_context is flattened+percent-encoded. Decode and compare as a map
	// since only pair-ordering within the flat string is deterministic — map
	// equality is what the handler ultimately depends on.
	flat := node.Attrs["steer_context"]
	if flat == "" {
		t.Fatalf("mgr.Attrs[steer_context] is empty; want encoded k=v,k=v")
	}

	// Sanity check: reserved chars must be percent-encoded in the flat form so
	// round-trips through the pair splitter stay lossless.
	if strings.Contains(flat, "speed,up") {
		t.Errorf("steer_context flat form %q leaks literal ',' — expected %%2C", flat)
	}
	if strings.Contains(flat, "high=critical") {
		t.Errorf("steer_context flat form %q leaks literal '=' — expected %%3D", flat)
	}
	if !strings.Contains(flat, "50%25off") {
		t.Errorf("steer_context flat form %q missing percent-encoded '%%' (expected 50%%25off)", flat)
	}

	// Decode and verify map equality.
	got := decodeFlatSteerContextForTest(flat)
	if !reflect.DeepEqual(got, steerContext) {
		t.Errorf("decoded steer_context = %v, want %v", got, steerContext)
	}
}

// TestFromDippinIR_ManagerLoop_EmptyOptionalFields verifies omit-on-zero.
// A ManagerLoopConfig with only SubgraphRef should not produce attrs for the
// unset fields, so the handler applies its defaults (45s poll, 1000 cycles).
func TestFromDippinIR_ManagerLoop_EmptyOptionalFields(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "MgrLoopEmpty",
		Start: "start",
		Exit:  "exit",
		Nodes: []*ir.Node{
			{ID: "start", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			{
				ID:   "mgr",
				Kind: ir.NodeManagerLoop,
				Config: ir.ManagerLoopConfig{
					SubgraphRef: "./child.dip",
				},
			},
			{ID: "exit", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
		},
		Edges: []*ir.Edge{
			{From: "start", To: "mgr"},
			{From: "mgr", To: "exit"},
		},
	}

	g, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}
	node := g.Nodes["mgr"]
	if node.Attrs["subgraph_ref"] != "./child.dip" {
		t.Errorf("subgraph_ref = %q, want %q", node.Attrs["subgraph_ref"], "./child.dip")
	}
	for _, key := range []string{"poll_interval", "max_cycles", "stop_condition", "steer_condition", "steer_context"} {
		if v, ok := node.Attrs[key]; ok {
			t.Errorf("expected attr %q to be absent, got %q", key, v)
		}
	}
}

// TestFromDippinIR_ManagerLoop_ParsedConditionFallback verifies that when a
// Condition has only Parsed populated (no Raw), the adapter formats it back
// to text. This exercises the invariant noted in the issue comment — the
// parser populates Raw, simulate.EnsureConditionsParsed fills Parsed. In
// practice Raw will be set when the adapter runs, but the formatter fallback
// must be correct for synthetic/test IR paths that skip the parser.
func TestFromDippinIR_ManagerLoop_ParsedConditionFallback(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "MgrLoopParsed",
		Start: "start",
		Exit:  "exit",
		Nodes: []*ir.Node{
			{ID: "start", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			{
				ID:   "mgr",
				Kind: ir.NodeManagerLoop,
				Config: ir.ManagerLoopConfig{
					SubgraphRef: "./child.dip",
					StopCondition: &ir.Condition{
						Parsed: ir.CondCompare{Variable: "stack.child.cycles", Op: "=", Value: "3"},
					},
					SteerCondition: &ir.Condition{
						Parsed: ir.CondAnd{
							Left:  ir.CondCompare{Variable: "stack.child.cycles", Op: "=", Value: "1"},
							Right: ir.CondCompare{Variable: "stack.child.status", Op: "=", Value: "running"},
						},
					},
				},
			},
			{ID: "exit", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
		},
		Edges: []*ir.Edge{
			{From: "start", To: "mgr"},
			{From: "mgr", To: "exit"},
		},
	}

	g, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}
	node := g.Nodes["mgr"]
	if got, want := node.Attrs["stop_condition"], "stack.child.cycles = 3"; got != want {
		t.Errorf("stop_condition = %q, want %q", got, want)
	}
	if got, want := node.Attrs["steer_condition"], "stack.child.cycles = 1 && stack.child.status = running"; got != want {
		t.Errorf("steer_condition = %q, want %q", got, want)
	}
}

// TestFromDippinIR_ManagerLoopAsStart verifies the shape-override gotcha:
// when a manager_loop is the workflow's Start, ensureStartExitNodes stomps
// the shape to Mdiamond, but the handler and flat attrs must remain intact
// so the manager_loop semantics execute. Mirrors dippin-lang's
// migrate.resolveStartExitKind pattern from the inverse direction.
func TestFromDippinIR_ManagerLoopAsStart(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "MgrLoopAtStart",
		Start: "mgr",
		Exit:  "exit",
		Nodes: []*ir.Node{
			{
				ID:   "mgr",
				Kind: ir.NodeManagerLoop,
				Config: ir.ManagerLoopConfig{
					SubgraphRef:  "./child.dip",
					PollInterval: 5 * time.Second,
					MaxCycles:    7,
				},
			},
			{ID: "exit", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
		},
		Edges: []*ir.Edge{{From: "mgr", To: "exit"}},
	}

	g, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}
	node := g.Nodes["mgr"]

	// Shape IS stomped for Start nodes — this is the gotcha.
	if node.Shape != "Mdiamond" {
		t.Errorf("mgr.Shape = %q, want %q (start-marker override)", node.Shape, "Mdiamond")
	}
	// BUT handler must still route to stack.manager_loop so the supervisor runs.
	if node.Handler != "stack.manager_loop" {
		t.Errorf("mgr.Handler = %q, want %q (must survive start-marker stomp)", node.Handler, "stack.manager_loop")
	}
	// Flat attrs must be preserved regardless of shape.
	if node.Attrs["subgraph_ref"] != "./child.dip" {
		t.Errorf("subgraph_ref = %q, want %q", node.Attrs["subgraph_ref"], "./child.dip")
	}
	if node.Attrs["poll_interval"] != "5s" {
		t.Errorf("poll_interval = %q, want %q", node.Attrs["poll_interval"], "5s")
	}
	if node.Attrs["max_cycles"] != "7" {
		t.Errorf("max_cycles = %q, want %q", node.Attrs["max_cycles"], "7")
	}
}

// TestFromDippinIR_ManagerLoopAsExit mirrors the Start-override test for Exit.
// Exit uses Msquare instead of Mdiamond, but the same invariant holds:
// handler + attrs survive the shape override.
func TestFromDippinIR_ManagerLoopAsExit(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "MgrLoopAtExit",
		Start: "start",
		Exit:  "mgr",
		Nodes: []*ir.Node{
			{ID: "start", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			{
				ID:   "mgr",
				Kind: ir.NodeManagerLoop,
				Config: ir.ManagerLoopConfig{
					SubgraphRef: "./shutdown.dip",
					MaxCycles:   1,
				},
			},
		},
		Edges: []*ir.Edge{{From: "start", To: "mgr"}},
	}

	g, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}
	node := g.Nodes["mgr"]

	if node.Shape != "Msquare" {
		t.Errorf("mgr.Shape = %q, want %q (exit-marker override)", node.Shape, "Msquare")
	}
	if node.Handler != "stack.manager_loop" {
		t.Errorf("mgr.Handler = %q, want %q (must survive exit-marker stomp)", node.Handler, "stack.manager_loop")
	}
	if node.Attrs["subgraph_ref"] != "./shutdown.dip" {
		t.Errorf("subgraph_ref = %q, want %q", node.Attrs["subgraph_ref"], "./shutdown.dip")
	}
	if node.Attrs["max_cycles"] != "1" {
		t.Errorf("max_cycles = %q, want %q", node.Attrs["max_cycles"], "1")
	}
}

// TestFlattenSteerContext_Deterministic ensures flattened output is sorted
// alphabetically, matching dippin-lang v0.22.0 export.flattenSteerContext so
// round-trips (tracker adapter → DOT → dippin-lang migrate) are byte-identical.
func TestFlattenSteerContext_Deterministic(t *testing.T) {
	m := map[string]string{
		"z": "last",
		"a": "first",
		"m": "middle",
	}
	got := flattenSteerContext(m)
	want := "a=first,m=middle,z=last"
	if got != want {
		t.Errorf("flattenSteerContext = %q, want %q", got, want)
	}
}

// TestFlattenSteerContext_EmptyMap documents the empty-map convention:
// empty input → empty string so callers can suppress the attr entirely.
func TestFlattenSteerContext_EmptyMap(t *testing.T) {
	if got := flattenSteerContext(nil); got != "" {
		t.Errorf("flattenSteerContext(nil) = %q, want empty", got)
	}
	if got := flattenSteerContext(map[string]string{}); got != "" {
		t.Errorf("flattenSteerContext({}) = %q, want empty", got)
	}
}

// TestFlattenSteerContext_EncodesReservedInKeysAndValues verifies both keys
// and values get the three reserved chars escaped. Percent must go first so
// it isn't double-encoded by the subsequent "," / "=" replacements.
func TestFlattenSteerContext_EncodesReservedInKeysAndValues(t *testing.T) {
	// Key 'k,1' must encode to 'k%2C1'; value '50%off' to '50%25off';
	// another key with '=' to '...%3D...'. Sort order is by the ENCODED key
	// (matches dippin-lang's exporter).
	m := map[string]string{
		"k,1":     "v1",
		"k=2":     "v2",
		"percent": "50%off",
	}
	got := flattenSteerContext(m)
	// Encoded keys sort as: "k%2C1" < "k%3D2" < "percent"
	want := "k%2C1=v1,k%3D2=v2,percent=50%25off"
	if got != want {
		t.Errorf("flattenSteerContext = %q, want %q", got, want)
	}
}

// TestFormatManagerLoopCondition_EvaluatorCompatibility pins the critical
// invariant that formatter output is directly parseable by
// pipeline.EvaluateCondition. A Parsed-only ir.Condition (no Raw) gets
// formatted on the fly; if the formatter emits English `and`/`or` tokens,
// the evaluator — which only recognizes Go-style `&&`/`||` — would silently
// mis-evaluate the expression as a single opaque clause. This test formats
// each binary + negation case and runs the result through the evaluator to
// prove the round-trip is correct.
//
// Closes part of #172 (CondOr / CondNot coverage) and the Codex P2 finding
// from PR #170 round-2 review.
func TestFormatManagerLoopCondition_EvaluatorCompatibility(t *testing.T) {
	// Seed a context with two keys the compare clauses will read. Note the
	// formatter strips the "ctx." prefix from variable names, so the
	// evaluator sees bare `outcome` / `status` lookups.
	pctx := NewPipelineContext()
	pctx.Set("outcome", "success")
	pctx.Set("status", "running")

	cases := []struct {
		name string
		expr ir.ConditionExpr
		want bool
	}{
		{
			name: "CondAnd both true",
			expr: ir.CondAnd{
				Left:  ir.CondCompare{Variable: "ctx.outcome", Op: "=", Value: "success"},
				Right: ir.CondCompare{Variable: "ctx.status", Op: "=", Value: "running"},
			},
			want: true,
		},
		{
			name: "CondAnd one false",
			expr: ir.CondAnd{
				Left:  ir.CondCompare{Variable: "ctx.outcome", Op: "=", Value: "success"},
				Right: ir.CondCompare{Variable: "ctx.status", Op: "=", Value: "stopped"},
			},
			want: false,
		},
		{
			name: "CondOr first true",
			expr: ir.CondOr{
				Left:  ir.CondCompare{Variable: "ctx.outcome", Op: "=", Value: "success"},
				Right: ir.CondCompare{Variable: "ctx.status", Op: "=", Value: "stopped"},
			},
			want: true,
		},
		{
			name: "CondOr both false",
			expr: ir.CondOr{
				Left:  ir.CondCompare{Variable: "ctx.outcome", Op: "=", Value: "fail"},
				Right: ir.CondCompare{Variable: "ctx.status", Op: "=", Value: "stopped"},
			},
			want: false,
		},
		{
			name: "CondNot wrapping CondCompare (true → false)",
			expr: ir.CondNot{
				Inner: ir.CondCompare{Variable: "ctx.outcome", Op: "=", Value: "success"},
			},
			want: false,
		},
		{
			name: "CondNot wrapping CondCompare (false → true)",
			expr: ir.CondNot{
				Inner: ir.CondCompare{Variable: "ctx.outcome", Op: "=", Value: "fail"},
			},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			formatted := formatManagerLoopCondition(tc.expr)
			if formatted == "" {
				t.Fatalf("formatter returned empty for %v", tc.expr)
			}
			got, err := EvaluateCondition(formatted, pctx)
			if err != nil {
				t.Fatalf("EvaluateCondition(%q) returned error: %v — formatter emitted tokens the evaluator cannot parse", formatted, err)
			}
			if got != tc.want {
				t.Errorf("EvaluateCondition(%q) = %v, want %v", formatted, got, tc.want)
			}
		})
	}
}

// decodeFlatSteerContextForTest is a local decoder mirroring the handler's
// parseSteerContext — we duplicate it here rather than cross-importing
// handlers (which would create a cycle) so the adapter test stays self-contained.
func decodeFlatSteerContextForTest(s string) map[string]string {
	out := map[string]string{}
	if s == "" {
		return out
	}
	decoder := strings.NewReplacer("%25", "%", "%2C", ",", "%3D", "=")
	for _, pair := range strings.Split(s, ",") {
		kv := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(kv) != 2 {
			continue
		}
		k := decoder.Replace(strings.TrimSpace(kv[0]))
		v := decoder.Replace(strings.TrimSpace(kv[1]))
		out[k] = v
	}
	return out
}
