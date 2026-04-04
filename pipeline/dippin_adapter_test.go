// ABOUTME: Comprehensive tests for the Dippin IR to Graph adapter.
// ABOUTME: Validates field mappings, node kind conversions, and round-trip fidelity.
package pipeline

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/dippin-lang/ir"
)

// TestFromDippinIR_Minimal verifies the adapter handles a minimal valid workflow.
func TestFromDippinIR_Minimal(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "MinimalWorkflow",
		Start: "start",
		Exit:  "exit",
		Nodes: []*ir.Node{
			{
				ID:     "start",
				Kind:   ir.NodeAgent,
				Label:  "Start",
				Config: ir.AgentConfig{},
			},
			{
				ID:     "exit",
				Kind:   ir.NodeAgent,
				Label:  "Exit",
				Config: ir.AgentConfig{},
			},
		},
		Edges: []*ir.Edge{
			{From: "start", To: "exit"},
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}

	// Verify basic properties
	if graph.Name != "MinimalWorkflow" {
		t.Errorf("graph.Name = %q, want %q", graph.Name, "MinimalWorkflow")
	}
	if graph.StartNode != "start" {
		t.Errorf("graph.StartNode = %q, want %q", graph.StartNode, "start")
	}
	if graph.ExitNode != "exit" {
		t.Errorf("graph.ExitNode = %q, want %q", graph.ExitNode, "exit")
	}

	// Verify nodes exist
	if len(graph.Nodes) != 2 {
		t.Errorf("len(graph.Nodes) = %d, want 2", len(graph.Nodes))
	}

	// Verify start node shape is overridden to Mdiamond
	startNode := graph.Nodes["start"]
	if startNode.Shape != "Mdiamond" {
		t.Errorf("start node shape = %q, want %q", startNode.Shape, "Mdiamond")
	}

	// Verify exit node shape is overridden to Msquare
	exitNode := graph.Nodes["exit"]
	if exitNode.Shape != "Msquare" {
		t.Errorf("exit node shape = %q, want %q", exitNode.Shape, "Msquare")
	}

	// Verify edge exists
	if len(graph.Edges) != 1 {
		t.Fatalf("len(graph.Edges) = %d, want 1", len(graph.Edges))
	}
	edge := graph.Edges[0]
	if edge.From != "start" || edge.To != "exit" {
		t.Errorf("edge = %s -> %s, want start -> exit", edge.From, edge.To)
	}
}

// TestFromDippinIR_AllNodeKinds verifies all node kinds map to correct shapes.
func TestFromDippinIR_AllNodeKinds(t *testing.T) {
	testCases := []struct {
		kind          ir.NodeKind
		expectedShape string
		config        ir.NodeConfig
	}{
		{ir.NodeAgent, "box", ir.AgentConfig{}},
		{ir.NodeHuman, "hexagon", ir.HumanConfig{}},
		{ir.NodeTool, "parallelogram", ir.ToolConfig{}},
		{ir.NodeParallel, "component", ir.ParallelConfig{}},
		{ir.NodeFanIn, "tripleoctagon", ir.FanInConfig{}},
		{ir.NodeSubgraph, "tab", ir.SubgraphConfig{}},
		{ir.NodeConditional, "diamond", ir.ConditionalConfig{}},
	}

	for _, tc := range testCases {
		t.Run(string(tc.kind), func(t *testing.T) {
			workflow := &ir.Workflow{
				Name:  "TestWorkflow",
				Start: "start",
				Exit:  "exit",
				Nodes: []*ir.Node{
					{
						ID:     "start",
						Kind:   ir.NodeAgent,
						Config: ir.AgentConfig{},
					},
					{
						ID:     "test_node",
						Kind:   tc.kind,
						Label:  "Test Node",
						Config: tc.config,
					},
					{
						ID:     "exit",
						Kind:   ir.NodeAgent,
						Config: ir.AgentConfig{},
					},
				},
				Edges: []*ir.Edge{
					{From: "start", To: "test_node"},
					{From: "test_node", To: "exit"},
				},
			}

			graph, err := FromDippinIR(workflow)
			if err != nil {
				t.Fatalf("FromDippinIR failed: %v", err)
			}

			node := graph.Nodes["test_node"]
			if node == nil {
				t.Fatalf("node test_node not found")
			}

			if node.Shape != tc.expectedShape {
				t.Errorf("node.Shape = %q, want %q", node.Shape, tc.expectedShape)
			}

			// Verify handler is resolved
			expectedHandler, _ := ShapeToHandler(tc.expectedShape)
			if node.Handler != expectedHandler {
				t.Errorf("node.Handler = %q, want %q", node.Handler, expectedHandler)
			}
		})
	}
}

// TestFromDippinIR_AgentConfig verifies AgentConfig fields are extracted correctly.
func TestFromDippinIR_AgentConfig(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "AgentTest",
		Start: "start",
		Exit:  "agent",
		Nodes: []*ir.Node{
			{
				ID:     "start",
				Kind:   ir.NodeAgent,
				Config: ir.AgentConfig{},
			},
			{
				ID:    "agent",
				Kind:  ir.NodeAgent,
				Label: "Complex Agent",
				Config: ir.AgentConfig{
					Prompt:              "Analyze the code",
					SystemPrompt:        "You are a code reviewer",
					Model:               "claude-3.5-sonnet",
					Provider:            "anthropic",
					MaxTurns:            5,
					CmdTimeout:          30 * time.Second,
					CacheTools:          true,
					Compaction:          "aggressive",
					CompactionThreshold: 0.75,
					ReasoningEffort:     "high",
					Fidelity:            "strict",
					AutoStatus:          true,
					GoalGate:            true,
				},
			},
		},
		Edges: []*ir.Edge{
			{From: "start", To: "agent"},
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}

	node := graph.Nodes["agent"]
	attrs := node.Attrs

	// Verify all agent config fields
	tests := []struct {
		key   string
		value string
	}{
		{"prompt", "Analyze the code"},
		{"system_prompt", "You are a code reviewer"},
		{"llm_model", "claude-3.5-sonnet"},
		{"llm_provider", "anthropic"},
		{"max_turns", "5"},
		{"command_timeout", "30s"},
		{"cache_tool_results", "true"},
		{"context_compaction", "aggressive"},
		{"context_compaction_threshold", "0.75"},
		{"reasoning_effort", "high"},
		{"fidelity", "strict"},
		{"auto_status", "true"},
		{"goal_gate", "true"},
	}

	for _, tt := range tests {
		if attrs[tt.key] != tt.value {
			t.Errorf("attrs[%q] = %q, want %q", tt.key, attrs[tt.key], tt.value)
		}
	}
}

// TestFromDippinIR_HumanConfig verifies HumanConfig extraction.
func TestFromDippinIR_HumanConfig(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "HumanTest",
		Start: "start",
		Exit:  "human",
		Nodes: []*ir.Node{
			{
				ID:     "start",
				Kind:   ir.NodeAgent,
				Config: ir.AgentConfig{},
			},
			{
				ID:    "human",
				Kind:  ir.NodeHuman,
				Label: "Approve?",
				Config: ir.HumanConfig{
					Mode:    "choice",
					Default: "yes",
				},
			},
		},
		Edges: []*ir.Edge{
			{From: "start", To: "human"},
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}

	node := graph.Nodes["human"]
	if node.Attrs["mode"] != "choice" {
		t.Errorf("mode = %q, want %q", node.Attrs["mode"], "choice")
	}
	if node.Attrs["default_choice"] != "yes" {
		t.Errorf("default_choice = %q, want %q", node.Attrs["default_choice"], "yes")
	}
}

// TestFromDippinIR_ToolConfig verifies ToolConfig extraction.
func TestFromDippinIR_ToolConfig(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "ToolTest",
		Start: "start",
		Exit:  "tool",
		Nodes: []*ir.Node{
			{
				ID:     "start",
				Kind:   ir.NodeAgent,
				Config: ir.AgentConfig{},
			},
			{
				ID:    "tool",
				Kind:  ir.NodeTool,
				Label: "Run Tests",
				Config: ir.ToolConfig{
					Command: "go test ./...",
					Timeout: 60 * time.Second,
				},
			},
		},
		Edges: []*ir.Edge{
			{From: "start", To: "tool"},
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}

	node := graph.Nodes["tool"]
	if node.Attrs["tool_command"] != "go test ./..." {
		t.Errorf("tool_command = %q, want %q", node.Attrs["tool_command"], "go test ./...")
	}
	if node.Attrs["timeout"] != "1m0s" {
		t.Errorf("timeout = %q, want %q", node.Attrs["timeout"], "1m0s")
	}
}

// TestFromDippinIR_ParallelConfig verifies ParallelConfig extraction.
func TestFromDippinIR_ParallelConfig(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "ParallelTest",
		Start: "start",
		Exit:  "exit",
		Nodes: []*ir.Node{
			{
				ID:     "start",
				Kind:   ir.NodeAgent,
				Config: ir.AgentConfig{},
			},
			{
				ID:    "parallel",
				Kind:  ir.NodeParallel,
				Label: "Fan Out",
				Config: ir.ParallelConfig{
					Targets: []string{"task1", "task2", "task3"},
				},
			},
			{
				ID:     "exit",
				Kind:   ir.NodeAgent,
				Config: ir.AgentConfig{},
			},
		},
		Edges: []*ir.Edge{
			{From: "start", To: "parallel"},
			{From: "parallel", To: "exit"},
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}

	node := graph.Nodes["parallel"]
	expected := "task1,task2,task3"
	if node.Attrs["parallel_targets"] != expected {
		t.Errorf("parallel_targets = %q, want %q", node.Attrs["parallel_targets"], expected)
	}
}

// TestFromDippinIR_FanInConfig verifies FanInConfig extraction.
func TestFromDippinIR_FanInConfig(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "FanInTest",
		Start: "start",
		Exit:  "exit",
		Nodes: []*ir.Node{
			{
				ID:     "start",
				Kind:   ir.NodeAgent,
				Config: ir.AgentConfig{},
			},
			{
				ID:    "fanin",
				Kind:  ir.NodeFanIn,
				Label: "Join",
				Config: ir.FanInConfig{
					Sources: []string{"task1", "task2"},
				},
			},
			{
				ID:     "exit",
				Kind:   ir.NodeAgent,
				Config: ir.AgentConfig{},
			},
		},
		Edges: []*ir.Edge{
			{From: "start", To: "fanin"},
			{From: "fanin", To: "exit"},
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}

	node := graph.Nodes["fanin"]
	expected := "task1,task2"
	if node.Attrs["fan_in_sources"] != expected {
		t.Errorf("fan_in_sources = %q, want %q", node.Attrs["fan_in_sources"], expected)
	}
}

// TestFromDippinIR_SubgraphConfig verifies SubgraphConfig extraction.
func TestFromDippinIR_SubgraphConfig(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "SubgraphTest",
		Start: "start",
		Exit:  "exit",
		Nodes: []*ir.Node{
			{
				ID:     "start",
				Kind:   ir.NodeAgent,
				Config: ir.AgentConfig{},
			},
			{
				ID:    "subgraph",
				Kind:  ir.NodeSubgraph,
				Label: "Run Subtask",
				Config: ir.SubgraphConfig{
					Ref: "subtask.dip",
					Params: map[string]string{
						"env":     "prod",
						"timeout": "30s",
					},
				},
			},
			{
				ID:     "exit",
				Kind:   ir.NodeAgent,
				Config: ir.AgentConfig{},
			},
		},
		Edges: []*ir.Edge{
			{From: "start", To: "subgraph"},
			{From: "subgraph", To: "exit"},
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}

	node := graph.Nodes["subgraph"]
	if node.Attrs["subgraph_ref"] != "subtask.dip" {
		t.Errorf("subgraph_ref = %q, want %q", node.Attrs["subgraph_ref"], "subtask.dip")
	}

	// Params are serialized as sorted comma-separated key=value pairs
	params := node.Attrs["subgraph_params"]
	want := "env=prod,timeout=30s"
	if params != want {
		t.Errorf("subgraph_params = %q, want %q", params, want)
	}
}

// TestFromDippinIR_WorkflowDefaults verifies workflow-level defaults are mapped to graph attrs.
func TestFromDippinIR_WorkflowDefaults(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "DefaultsTest",
		Start: "start",
		Exit:  "exit",
		Defaults: ir.WorkflowDefaults{
			Model:         "gpt-4",
			Provider:      "openai",
			RetryPolicy:   "standard",
			MaxRetries:    3,
			Fidelity:      "strict",
			MaxRestarts:   10,
			RestartTarget: "start",
			CacheTools:    true,
			Compaction:    "conservative",
		},
		Nodes: []*ir.Node{
			{
				ID:     "start",
				Kind:   ir.NodeAgent,
				Config: ir.AgentConfig{},
			},
			{
				ID:     "exit",
				Kind:   ir.NodeAgent,
				Config: ir.AgentConfig{},
			},
		},
		Edges: []*ir.Edge{
			{From: "start", To: "exit"},
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}

	attrs := graph.Attrs
	tests := []struct {
		key   string
		value string
	}{
		{"llm_model", "gpt-4"},
		{"llm_provider", "openai"},
		{"default_retry_policy", "standard"},
		{"default_max_retry", "3"},
		{"default_fidelity", "strict"},
		{"max_restarts", "10"},
		{"restart_target", "start"},
		{"cache_tool_results", "true"},
		{"context_compaction", "conservative"},
	}

	for _, tt := range tests {
		if attrs[tt.key] != tt.value {
			t.Errorf("attrs[%q] = %q, want %q", tt.key, attrs[tt.key], tt.value)
		}
	}
}

// TestFromDippinIR_EdgeConditions verifies edge conditions are preserved as raw strings.
func TestFromDippinIR_EdgeConditions(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "ConditionTest",
		Start: "start",
		Exit:  "exit",
		Nodes: []*ir.Node{
			{
				ID:     "start",
				Kind:   ir.NodeAgent,
				Config: ir.AgentConfig{},
			},
			{
				ID:     "branch",
				Kind:   ir.NodeAgent,
				Config: ir.AgentConfig{},
			},
			{
				ID:     "exit",
				Kind:   ir.NodeAgent,
				Config: ir.AgentConfig{},
			},
		},
		Edges: []*ir.Edge{
			{From: "start", To: "branch"},
			{
				From:  "branch",
				To:    "exit",
				Label: "success",
				Condition: &ir.Condition{
					Raw: "ctx.status == \"success\"",
				},
			},
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}

	// Find the conditional edge
	var condEdge *Edge
	for _, e := range graph.Edges {
		if e.Condition != "" {
			condEdge = e
			break
		}
	}

	if condEdge == nil {
		t.Fatalf("conditional edge not found")
	}

	expected := "ctx.status == \"success\""
	if condEdge.Condition != expected {
		t.Errorf("edge.Condition = %q, want %q", condEdge.Condition, expected)
	}
	if condEdge.Label != "success" {
		t.Errorf("edge.Label = %q, want %q", condEdge.Label, "success")
	}
}

// TestFromDippinIR_RetryConfig verifies retry configuration is extracted.
func TestFromDippinIR_RetryConfig(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "RetryTest",
		Start: "start",
		Exit:  "exit",
		Nodes: []*ir.Node{
			{
				ID:     "start",
				Kind:   ir.NodeAgent,
				Config: ir.AgentConfig{},
			},
			{
				ID:    "flaky",
				Kind:  ir.NodeTool,
				Label: "Flaky Tool",
				Config: ir.ToolConfig{
					Command: "flaky-script.sh",
				},
				Retry: ir.RetryConfig{
					Policy:         "aggressive",
					MaxRetries:     5,
					RetryTarget:    "start",
					FallbackTarget: "exit",
				},
			},
			{
				ID:     "exit",
				Kind:   ir.NodeAgent,
				Config: ir.AgentConfig{},
			},
		},
		Edges: []*ir.Edge{
			{From: "start", To: "flaky"},
			{From: "flaky", To: "exit"},
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}

	node := graph.Nodes["flaky"]
	attrs := node.Attrs

	tests := []struct {
		key   string
		value string
	}{
		{"retry_policy", "aggressive"},
		{"max_retries", "5"},
		{"retry_target", "start"},
		{"fallback_retry_target", "exit"},
	}

	for _, tt := range tests {
		if attrs[tt.key] != tt.value {
			t.Errorf("attrs[%q] = %q, want %q", tt.key, attrs[tt.key], tt.value)
		}
	}
}

func TestFromDippinIR_RetryPolicy(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "RetryPolicyTest",
		Start: "start",
		Exit:  "exit",
		Nodes: []*ir.Node{
			{ID: "start", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			{
				ID:   "worker",
				Kind: ir.NodeAgent,
				Config: ir.AgentConfig{
					Prompt: "do work",
				},
				Retry: ir.RetryConfig{
					Policy: "aggressive",
				},
			},
			{ID: "exit", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
		},
		Edges: []*ir.Edge{
			{From: "start", To: "worker"},
			{From: "worker", To: "exit"},
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}

	node := graph.Nodes["worker"]
	if got := node.Attrs["retry_policy"]; got != "aggressive" {
		t.Errorf("retry_policy = %q, want %q", got, "aggressive")
	}

	// Verify it integrates with ResolveRetryPolicy.
	policy := ResolveRetryPolicy(node, graph.Attrs)
	if policy.Name != "aggressive" {
		t.Errorf("resolved policy Name = %q, want %q", policy.Name, "aggressive")
	}
}

func TestFromDippinIR_RetryEmptyPolicyOmitted(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "NoPolicySet",
		Start: "start",
		Exit:  "exit",
		Nodes: []*ir.Node{
			{ID: "start", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			{
				ID:   "worker",
				Kind: ir.NodeAgent,
				Config: ir.AgentConfig{
					Prompt: "do work",
				},
				Retry: ir.RetryConfig{},
			},
			{ID: "exit", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
		},
		Edges: []*ir.Edge{
			{From: "start", To: "worker"},
			{From: "worker", To: "exit"},
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}

	node := graph.Nodes["worker"]
	if _, ok := node.Attrs["retry_policy"]; ok {
		t.Errorf("expected no retry_policy attr when empty, got %q", node.Attrs["retry_policy"])
	}
}

// TestFromDippinIR_Errors verifies error handling.
func TestFromDippinIR_Errors(t *testing.T) {
	tests := []struct {
		name     string
		workflow *ir.Workflow
		wantErr  string
	}{
		{
			name:     "nil workflow",
			workflow: nil,
			wantErr:  "nil workflow",
		},
		{
			name: "missing start",
			workflow: &ir.Workflow{
				Name:  "MissingStart",
				Exit:  "exit",
				Nodes: []*ir.Node{},
			},
			wantErr: "workflow missing Start node",
		},
		{
			name: "missing exit",
			workflow: &ir.Workflow{
				Name:  "MissingExit",
				Start: "start",
				Nodes: []*ir.Node{},
			},
			wantErr: "workflow missing Exit node",
		},
		{
			name: "start node doesn't exist",
			workflow: &ir.Workflow{
				Name:  "NoStart",
				Start: "missing",
				Exit:  "exit",
				Nodes: []*ir.Node{
					{
						ID:     "exit",
						Kind:   ir.NodeAgent,
						Config: ir.AgentConfig{},
					},
				},
			},
			wantErr: "start node \"missing\" not found",
		},
		{
			name: "exit node doesn't exist",
			workflow: &ir.Workflow{
				Name:  "NoExit",
				Start: "start",
				Exit:  "missing",
				Nodes: []*ir.Node{
					{
						ID:     "start",
						Kind:   ir.NodeAgent,
						Config: ir.AgentConfig{},
					},
				},
			},
			wantErr: "exit node \"missing\" not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := FromDippinIR(tt.workflow)
			if err == nil {
				t.Fatalf("FromDippinIR succeeded, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestSynthesizeImplicitEdges_ParallelFanIn verifies that implicit edges are
// synthesized for parallel fan-out targets and fan-in sources when they are
// not already present as explicit edges.
func TestSynthesizeImplicitEdges_ParallelFanIn(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "ParallelFanInTest",
		Start: "start",
		Exit:  "exit",
		Nodes: []*ir.Node{
			{ID: "start", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			{
				ID:   "dispatch",
				Kind: ir.NodeParallel,
				Config: ir.ParallelConfig{
					Targets: []string{"branch_a", "branch_b"},
				},
			},
			{ID: "branch_a", Kind: ir.NodeAgent, Config: ir.AgentConfig{Prompt: "task A"}},
			{ID: "branch_b", Kind: ir.NodeAgent, Config: ir.AgentConfig{Prompt: "task B"}},
			{
				ID:   "join",
				Kind: ir.NodeFanIn,
				Config: ir.FanInConfig{
					Sources: []string{"branch_a", "branch_b"},
				},
			},
			{ID: "exit", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
		},
		Edges: []*ir.Edge{
			{From: "start", To: "dispatch"},
			{From: "join", To: "exit"},
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}

	// Verify implicit edges were synthesized.
	edgeSet := make(map[[2]string]bool)
	for _, e := range graph.Edges {
		edgeSet[[2]string{e.From, e.To}] = true
	}

	// Parallel -> targets
	if !edgeSet[[2]string{"dispatch", "branch_a"}] {
		t.Error("missing implicit edge: dispatch -> branch_a")
	}
	if !edgeSet[[2]string{"dispatch", "branch_b"}] {
		t.Error("missing implicit edge: dispatch -> branch_b")
	}
	// Targets -> fan-in
	if !edgeSet[[2]string{"branch_a", "join"}] {
		t.Error("missing implicit edge: branch_a -> join")
	}
	if !edgeSet[[2]string{"branch_b", "join"}] {
		t.Error("missing implicit edge: branch_b -> join")
	}
	// Parallel -> join (for parallel_join attr)
	if !edgeSet[[2]string{"dispatch", "join"}] {
		t.Error("missing implicit edge: dispatch -> join")
	}

	// Verify parallel_join attr was set.
	dispatchNode := graph.Nodes["dispatch"]
	if dispatchNode.Attrs["parallel_join"] != "join" {
		t.Errorf("parallel_join = %q, want %q", dispatchNode.Attrs["parallel_join"], "join")
	}
}

// TestSynthesizeImplicitEdges_NoDuplicates verifies that synthesizeImplicitEdges
// does not create duplicate edges when explicit edges already exist.
func TestSynthesizeImplicitEdges_NoDuplicates(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "NoDupTest",
		Start: "start",
		Exit:  "exit",
		Nodes: []*ir.Node{
			{ID: "start", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			{
				ID:   "dispatch",
				Kind: ir.NodeParallel,
				Config: ir.ParallelConfig{
					Targets: []string{"branch_a"},
				},
			},
			{ID: "branch_a", Kind: ir.NodeAgent, Config: ir.AgentConfig{Prompt: "A"}},
			{
				ID:   "join",
				Kind: ir.NodeFanIn,
				Config: ir.FanInConfig{
					Sources: []string{"branch_a"},
				},
			},
			{ID: "exit", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
		},
		Edges: []*ir.Edge{
			{From: "start", To: "dispatch"},
			// Explicit edges that overlap with what synthesize would create.
			{From: "dispatch", To: "branch_a"},
			{From: "branch_a", To: "join"},
			{From: "join", To: "exit"},
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}

	// Count edges from dispatch -> branch_a — should be exactly 1.
	count := 0
	for _, e := range graph.Edges {
		if e.From == "dispatch" && e.To == "branch_a" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 edge dispatch->branch_a, got %d", count)
	}
}

// TestEnsureStartExitNodes_PreservesAgentHandler verifies that agent nodes
// designated as start/exit retain their codergen handler.
func TestEnsureStartExitNodes_PreservesAgentHandler(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "PreserveHandlerTest",
		Start: "Think",
		Exit:  "Done",
		Nodes: []*ir.Node{
			{ID: "Think", Kind: ir.NodeAgent, Config: ir.AgentConfig{Prompt: "What is 2+2?"}},
			{ID: "Done", Kind: ir.NodeAgent, Config: ir.AgentConfig{Prompt: "Say done."}},
		},
		Edges: []*ir.Edge{
			{From: "Think", To: "Done"},
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}

	startNode := graph.Nodes["Think"]
	if startNode.Handler != "codergen" {
		t.Errorf("start agent handler = %q, want codergen", startNode.Handler)
	}

	exitNode := graph.Nodes["Done"]
	if exitNode.Handler != "codergen" {
		t.Errorf("exit agent handler = %q, want codergen", exitNode.Handler)
	}
}

// TestEnsureStartExitNodes_MixedAgentAndSynthetic verifies the mixed case:
// agent start node preserves codergen, synthetic exit node gets exit handler.
func TestEnsureStartExitNodes_MixedAgentAndSynthetic(t *testing.T) {
	g := &Graph{
		Nodes:     make(map[string]*Node),
		StartNode: "agent_start",
		ExitNode:  "synthetic_end",
	}
	g.Nodes["agent_start"] = &Node{
		ID: "agent_start", Shape: "box", Handler: "codergen",
		Attrs: map[string]string{"prompt": "Do something."},
	}
	g.Nodes["synthetic_end"] = &Node{
		ID: "synthetic_end", Shape: "box", Handler: "",
		Attrs: make(map[string]string),
	}
	g.Edges = []*Edge{{From: "agent_start", To: "synthetic_end"}}

	err := ensureStartExitNodes(g)
	if err != nil {
		t.Fatalf("ensureStartExitNodes failed: %v", err)
	}

	if g.Nodes["agent_start"].Handler != "codergen" {
		t.Errorf("agent start handler = %q, want codergen", g.Nodes["agent_start"].Handler)
	}
	if g.Nodes["synthetic_end"].Handler != "exit" {
		t.Errorf("synthetic exit handler = %q, want exit", g.Nodes["synthetic_end"].Handler)
	}
	if g.Nodes["synthetic_end"].Shape != "Msquare" {
		t.Errorf("synthetic exit shape = %q, want Msquare", g.Nodes["synthetic_end"].Shape)
	}
}

// TestEnsureStartExitNodes_SetsHandlerWhenNoPrompt verifies that nodes without
// a prompt attribute get the start/exit handler and shape assigned.
func TestEnsureStartExitNodes_SetsHandlerWhenNoPrompt(t *testing.T) {
	g := &Graph{
		Nodes:     make(map[string]*Node),
		StartNode: "begin",
		ExitNode:  "end",
	}
	g.Nodes["begin"] = &Node{ID: "begin", Shape: "box", Handler: "", Attrs: make(map[string]string)}
	g.Nodes["end"] = &Node{ID: "end", Shape: "box", Handler: "", Attrs: make(map[string]string)}
	g.Edges = []*Edge{{From: "begin", To: "end"}}

	err := ensureStartExitNodes(g)
	if err != nil {
		t.Fatalf("ensureStartExitNodes failed: %v", err)
	}

	if g.Nodes["begin"].Handler != "start" {
		t.Errorf("begin handler = %q, want start", g.Nodes["begin"].Handler)
	}
	if g.Nodes["begin"].Shape != "Mdiamond" {
		t.Errorf("begin shape = %q, want Mdiamond", g.Nodes["begin"].Shape)
	}
	if g.Nodes["end"].Handler != "exit" {
		t.Errorf("end handler = %q, want exit", g.Nodes["end"].Handler)
	}
	if g.Nodes["end"].Shape != "Msquare" {
		t.Errorf("end shape = %q, want Msquare", g.Nodes["end"].Shape)
	}
}

// TestExtractAgentBackendAttrs_SimulatedIRParams verifies that agent backend
// attributes (model, provider, reasoning_effort, system_prompt) are correctly
// extracted from IR AgentConfig into graph node attrs, simulating the params
// that CodergenHandler reads at runtime.
func TestExtractAgentBackendAttrs_SimulatedIRParams(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "BackendAttrsTest",
		Start: "start",
		Exit:  "agent",
		Nodes: []*ir.Node{
			{ID: "start", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			{
				ID:    "agent",
				Kind:  ir.NodeAgent,
				Label: "Backend Agent",
				Config: ir.AgentConfig{
					Prompt:          "Build the feature",
					Model:           "claude-opus-4",
					Provider:        "anthropic",
					SystemPrompt:    "You are a senior engineer",
					ReasoningEffort: "high",
					MaxTurns:        100,
				},
			},
		},
		Edges: []*ir.Edge{
			{From: "start", To: "agent"},
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}

	node := graph.Nodes["agent"]
	tests := []struct {
		key   string
		value string
	}{
		{"prompt", "Build the feature"},
		{"llm_model", "claude-opus-4"},
		{"llm_provider", "anthropic"},
		{"system_prompt", "You are a senior engineer"},
		{"reasoning_effort", "high"},
		{"max_turns", "100"},
	}
	for _, tt := range tests {
		if node.Attrs[tt.key] != tt.value {
			t.Errorf("attrs[%q] = %q, want %q", tt.key, node.Attrs[tt.key], tt.value)
		}
	}
}

func TestEnsureStartExitNodes_ErrorMissingNodes(t *testing.T) {
	g := &Graph{
		Nodes:     make(map[string]*Node),
		StartNode: "missing_start",
		ExitNode:  "missing_exit",
	}

	err := ensureStartExitNodes(g)
	if err == nil {
		t.Fatal("expected error for missing start node, got nil")
	}
	if !strings.Contains(err.Error(), "missing_start") {
		t.Errorf("error should mention missing_start, got: %v", err)
	}

	// Add start but not exit — should error on exit.
	g.Nodes["missing_start"] = &Node{ID: "missing_start", Attrs: make(map[string]string)}
	err = ensureStartExitNodes(g)
	if err == nil {
		t.Fatal("expected error for missing exit node, got nil")
	}
	if !strings.Contains(err.Error(), "missing_exit") {
		t.Errorf("error should mention missing_exit, got: %v", err)
	}
}

// TestExtractHumanAttrs_Interview verifies all interview-mode fields are extracted.
func TestExtractHumanAttrs_Interview(t *testing.T) {
	cfg := ir.HumanConfig{
		Mode:         "interview",
		QuestionsKey: "my_questions",
		AnswersKey:   "my_answers",
		Prompt:       "Answer the questions below",
	}
	attrs := map[string]string{}
	extractHumanAttrs(cfg, attrs)

	if attrs["mode"] != "interview" {
		t.Errorf("expected mode 'interview', got %q", attrs["mode"])
	}
	if attrs["questions_key"] != "my_questions" {
		t.Errorf("expected questions_key 'my_questions', got %q", attrs["questions_key"])
	}
	if attrs["answers_key"] != "my_answers" {
		t.Errorf("expected answers_key 'my_answers', got %q", attrs["answers_key"])
	}
	if attrs["prompt"] != "Answer the questions below" {
		t.Errorf("expected prompt 'Answer the questions below', got %q", attrs["prompt"])
	}
}

// TestExtractHumanAttrs_InterviewDefaults verifies empty interview fields are not set.
func TestExtractHumanAttrs_InterviewDefaults(t *testing.T) {
	cfg := ir.HumanConfig{Mode: "interview"}
	attrs := map[string]string{}
	extractHumanAttrs(cfg, attrs)

	if _, ok := attrs["questions_key"]; ok {
		t.Error("empty QuestionsKey should not set attr")
	}
	if _, ok := attrs["answers_key"]; ok {
		t.Error("empty AnswersKey should not set attr")
	}
	if _, ok := attrs["prompt"]; ok {
		t.Error("empty Prompt should not set attr")
	}
}

func TestFromDippinIR_SentinelErrors(t *testing.T) {
	// nil workflow → ErrNilWorkflow
	_, err := FromDippinIR(nil)
	if !errors.Is(err, ErrNilWorkflow) {
		t.Errorf("nil workflow: got %v, want ErrNilWorkflow", err)
	}

	// missing Start → ErrMissingStart
	_, err = FromDippinIR(&ir.Workflow{Exit: "x"})
	if !errors.Is(err, ErrMissingStart) {
		t.Errorf("missing start: got %v, want ErrMissingStart", err)
	}

	// missing Exit → ErrMissingExit
	_, err = FromDippinIR(&ir.Workflow{Start: "s"})
	if !errors.Is(err, ErrMissingExit) {
		t.Errorf("missing exit: got %v, want ErrMissingExit", err)
	}

	// unknown node kind → ErrUnknownNodeKind
	_, err = FromDippinIR(&ir.Workflow{
		Name: "bad", Start: "s", Exit: "e",
		Nodes: []*ir.Node{{ID: "s", Kind: "bogus"}},
	})
	if !errors.Is(err, ErrUnknownNodeKind) {
		t.Errorf("unknown kind: got %v, want ErrUnknownNodeKind", err)
	}

	// ErrUnknownConfig is tested indirectly — it's only reachable if dippin-lang
	// adds a new NodeConfig implementation that tracker hasn't mapped yet.
	// We verify the sentinel exists and is usable with errors.Is.
	wrapped := fmt.Errorf("test: %w", ErrUnknownConfig)
	if !errors.Is(wrapped, ErrUnknownConfig) {
		t.Error("ErrUnknownConfig should be matchable via errors.Is")
	}
}

func TestFromDippinIR_NilNodeSkipped(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "nil-node",
		Start: "start",
		Exit:  "exit",
		Nodes: []*ir.Node{
			{ID: "start", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			nil, // should be skipped
			{ID: "exit", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
		},
		Edges: []*ir.Edge{
			{From: "start", To: "exit"},
			nil, // should be skipped
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}
	if len(graph.Nodes) != 2 {
		t.Errorf("len(graph.Nodes) = %d, want 2", len(graph.Nodes))
	}
	if len(graph.Edges) != 1 {
		t.Errorf("len(graph.Edges) = %d, want 1", len(graph.Edges))
	}
}

func TestExtractNodeAttrs_NilPointerConfig(t *testing.T) {
	attrs := map[string]string{}

	var agentCfg *ir.AgentConfig
	if err := extractNodeAttrs(agentCfg, attrs); err != nil {
		t.Errorf("nil *AgentConfig: unexpected error: %v", err)
	}

	var humanCfg *ir.HumanConfig
	if err := extractNodeAttrs(humanCfg, attrs); err != nil {
		t.Errorf("nil *HumanConfig: unexpected error: %v", err)
	}

	var toolCfg *ir.ToolConfig
	if err := extractNodeAttrs(toolCfg, attrs); err != nil {
		t.Errorf("nil *ToolConfig: unexpected error: %v", err)
	}

	var parallelCfg *ir.ParallelConfig
	if err := extractNodeAttrs(parallelCfg, attrs); err != nil {
		t.Errorf("nil *ParallelConfig: unexpected error: %v", err)
	}

	var fanInCfg *ir.FanInConfig
	if err := extractNodeAttrs(fanInCfg, attrs); err != nil {
		t.Errorf("nil *FanInConfig: unexpected error: %v", err)
	}

	var subgraphCfg *ir.SubgraphConfig
	if err := extractNodeAttrs(subgraphCfg, attrs); err != nil {
		t.Errorf("nil *SubgraphConfig: unexpected error: %v", err)
	}
}

func TestExtractSubgraphAttrs_DeterministicOrder(t *testing.T) {
	cfg := ir.SubgraphConfig{
		Ref: "my-subgraph",
		Params: map[string]string{
			"zebra":  "z",
			"alpha":  "a",
			"middle": "m",
		},
	}
	attrs := map[string]string{}
	extractSubgraphAttrs(cfg, attrs)
	want := "alpha=a,middle=m,zebra=z"
	if attrs["subgraph_params"] != want {
		t.Errorf("subgraph_params = %q, want %q", attrs["subgraph_params"], want)
	}

	// Run 10 times to verify determinism (Go randomizes map iteration).
	for i := 0; i < 10; i++ {
		attrs2 := map[string]string{}
		extractSubgraphAttrs(cfg, attrs2)
		if attrs2["subgraph_params"] != want {
			t.Errorf("iteration %d: subgraph_params = %q, want %q", i, attrs2["subgraph_params"], want)
		}
	}
}

func TestSerializeStylesheet_DeterministicOrder(t *testing.T) {
	rules := []ir.StylesheetRule{
		{
			Selector: ir.StyleSelector{Kind: "universal"},
			Properties: map[string]string{
				"z_prop": "z",
				"a_prop": "a",
			},
		},
	}
	result := serializeStylesheet(rules)
	// Properties should be sorted: a_prop before z_prop.
	aIdx := strings.Index(result, "a_prop")
	zIdx := strings.Index(result, "z_prop")
	if aIdx < 0 || zIdx < 0 {
		t.Fatalf("result = %q, missing properties", result)
	}
	if aIdx > zIdx {
		t.Errorf("properties not sorted: a_prop at %d, z_prop at %d in %q", aIdx, zIdx, result)
	}
}

func TestFromDippinIR_WorkflowVersionMapped(t *testing.T) {
	workflow := &ir.Workflow{
		Name:    "versioned",
		Start:   "start",
		Exit:    "exit",
		Version: "1",
		Nodes: []*ir.Node{
			{ID: "start", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			{ID: "exit", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
		},
		Edges: []*ir.Edge{
			{From: "start", To: "exit"},
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}
	if graph.Attrs["version"] != "1" {
		t.Errorf("graph.Attrs[version] = %q, want %q", graph.Attrs["version"], "1")
	}
}

func TestFromDippinIR_EmptyVersionOmitted(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "no-version",
		Start: "start",
		Exit:  "exit",
		Nodes: []*ir.Node{
			{ID: "start", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			{ID: "exit", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
		},
		Edges: []*ir.Edge{
			{From: "start", To: "exit"},
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}
	if _, ok := graph.Attrs["version"]; ok {
		t.Error("empty version should not be set in attrs")
	}
}

func TestExtractAgentAttrs_ZeroValueFieldsOmitted(t *testing.T) {
	attrs := map[string]string{}
	extractAgentAttrs(ir.AgentConfig{}, attrs)
	if len(attrs) != 0 {
		t.Errorf("zero-value AgentConfig produced %d attrs, want 0: %v", len(attrs), attrs)
	}
}

func TestExtractHumanAttrs_ZeroValueFieldsOmitted(t *testing.T) {
	attrs := map[string]string{}
	extractHumanAttrs(ir.HumanConfig{}, attrs)
	if len(attrs) != 0 {
		t.Errorf("zero-value HumanConfig produced %d attrs, want 0: %v", len(attrs), attrs)
	}
}

func TestExtractToolAttrs_ZeroValueFieldsOmitted(t *testing.T) {
	attrs := map[string]string{}
	extractToolAttrs(ir.ToolConfig{}, attrs)
	if len(attrs) != 0 {
		t.Errorf("zero-value ToolConfig produced %d attrs, want 0: %v", len(attrs), attrs)
	}
}

func TestConvertEdge_WeightAndRestart(t *testing.T) {
	irEdge := &ir.Edge{From: "a", To: "b", Weight: 5, Restart: true}
	gEdge := convertEdge(irEdge)
	if gEdge.Attrs["weight"] != "5" {
		t.Errorf("weight = %q, want %q", gEdge.Attrs["weight"], "5")
	}
	if gEdge.Attrs["restart"] != "true" {
		t.Errorf("restart = %q, want %q", gEdge.Attrs["restart"], "true")
	}
}

func TestConvertEdge_ZeroWeightOmitted(t *testing.T) {
	irEdge := &ir.Edge{From: "a", To: "b"}
	gEdge := convertEdge(irEdge)
	if _, ok := gEdge.Attrs["weight"]; ok {
		t.Error("zero weight should not be in attrs")
	}
	if _, ok := gEdge.Attrs["restart"]; ok {
		t.Error("false restart should not be in attrs")
	}
}
