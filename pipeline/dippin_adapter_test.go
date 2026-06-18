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
	"github.com/2389-research/dippin-lang/parser"
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

// TestFromDippinIR_HumanConfigTimeout verifies that dippin-lang v0.21.0's
// new HumanConfig.Timeout and HumanConfig.TimeoutAction fields land in
// node.Attrs as "timeout" and "timeout_action" — the keys tracker's
// pipeline/handlers/human.go already reads. Closes tracker#112.
func TestFromDippinIR_HumanConfigTimeout(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "HumanTimeoutTest",
		Start: "start",
		Exit:  "human",
		Nodes: []*ir.Node{
			{ID: "start", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			{
				ID:   "human",
				Kind: ir.NodeHuman,
				Config: ir.HumanConfig{
					Mode:          "choice",
					Timeout:       2 * time.Minute,
					TimeoutAction: "default",
				},
			},
		},
		Edges: []*ir.Edge{{From: "start", To: "human"}},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR: %v", err)
	}

	node := graph.Nodes["human"]
	if node.Attrs["timeout"] != "2m0s" {
		t.Errorf("timeout = %q, want 2m0s", node.Attrs["timeout"])
	}
	if node.Attrs["timeout_action"] != "default" {
		t.Errorf("timeout_action = %q, want default", node.Attrs["timeout_action"])
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

// TestFromDippinIR_ParallelFanInParams verifies that generic Params on
// parallel and fan_in IR nodes (dippin-lang v0.39.0) are forwarded into node
// attrs, with typed fields taking precedence over Params keys (#313).
func TestFromDippinIR_ParallelFanInParams(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "ParallelParamsTest",
		Start: "start",
		Exit:  "exit",
		Nodes: []*ir.Node{
			{ID: "start", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			{
				ID:   "parallel",
				Kind: ir.NodeParallel,
				Config: ir.ParallelConfig{
					Targets: []string{"task1", "task2"},
					Params: map[string]string{
						"fan_in_policy":    "all",
						"parallel_targets": "evil_override", // typed field must win
					},
				},
			},
			{ID: "task1", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			{ID: "task2", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			{
				ID:   "join",
				Kind: ir.NodeFanIn,
				Config: ir.FanInConfig{
					Sources: []string{"task1", "task2"},
					Params: map[string]string{
						"fan_in_policy": "quorum",
						"quorum":        "2",
					},
				},
			},
			{ID: "exit", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
		},
		Edges: []*ir.Edge{
			{From: "start", To: "parallel"},
			{From: "join", To: "exit"},
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}

	par := graph.Nodes["parallel"]
	if par.Attrs["fan_in_policy"] != "all" {
		t.Errorf("parallel fan_in_policy = %q, want all", par.Attrs["fan_in_policy"])
	}
	if par.Attrs["parallel_targets"] != "task1,task2" {
		t.Errorf("typed Targets must win over Params, got %q", par.Attrs["parallel_targets"])
	}

	join := graph.Nodes["join"]
	if join.Attrs["fan_in_policy"] != "quorum" || join.Attrs["quorum"] != "2" {
		t.Errorf("fan_in params not forwarded: policy=%q quorum=%q",
			join.Attrs["fan_in_policy"], join.Attrs["quorum"])
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
			Model:             "gpt-4",
			Provider:          "openai",
			RetryPolicy:       "standard",
			MaxRetries:        3,
			Fidelity:          "strict",
			MaxRestarts:       10,
			RestartTarget:     "start",
			CacheTools:        true,
			Compaction:        "conservative",
			ToolCommandsAllow: "git *,make *",
			ToolDenylistAdd:   "rm -rf *,dd *",
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
		{"tool_commands_allow", "git *,make *"},
		{"tool_denylist_add", "rm -rf *,dd *"},
	}

	for _, tt := range tests {
		if attrs[tt.key] != tt.value {
			t.Errorf("attrs[%q] = %q, want %q", tt.key, attrs[tt.key], tt.value)
		}
	}
}

// TestFromDippinIR_WorkflowBudgetDefaults verifies that dippin-lang v0.21.0's
// new WorkflowDefaults.MaxTotalTokens / MaxCostCents / MaxWallTime fields
// land in graph.Attrs with the corresponding keys. Closes tracker#67.
func TestFromDippinIR_WorkflowBudgetDefaults(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "BudgetTest",
		Start: "start",
		Exit:  "exit",
		Defaults: ir.WorkflowDefaults{
			MaxTotalTokens: 50000,
			MaxCostCents:   250,
			MaxWallTime:    15 * time.Minute,
			StallTimeout:   5 * time.Minute,
		},
		Nodes: []*ir.Node{
			{ID: "start", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			{ID: "exit", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
		},
		Edges: []*ir.Edge{{From: "start", To: "exit"}},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR: %v", err)
	}
	if graph.Attrs["max_total_tokens"] != "50000" {
		t.Errorf("max_total_tokens = %q, want 50000", graph.Attrs["max_total_tokens"])
	}
	if graph.Attrs["max_cost_cents"] != "250" {
		t.Errorf("max_cost_cents = %q, want 250", graph.Attrs["max_cost_cents"])
	}
	if graph.Attrs["max_wall_time"] != "15m0s" {
		t.Errorf("max_wall_time = %q, want 15m0s", graph.Attrs["max_wall_time"])
	}
	if graph.Attrs["stall_timeout"] != "5m0s" {
		t.Errorf("stall_timeout = %q, want 5m0s", graph.Attrs["stall_timeout"])
	}
}

// TestFromDippinIR_WorkflowBudgetUnsetOmitted verifies zero-value budget
// fields produce no graph.Attrs entry.
func TestFromDippinIR_WorkflowBudgetUnsetOmitted(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "NoBudgetTest",
		Start: "start",
		Exit:  "exit",
		Nodes: []*ir.Node{
			{ID: "start", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			{ID: "exit", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
		},
		Edges: []*ir.Edge{{From: "start", To: "exit"}},
	}
	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR: %v", err)
	}
	for _, k := range []string{"max_total_tokens", "max_cost_cents", "max_wall_time", "stall_timeout"} {
		if _, ok := graph.Attrs[k]; ok {
			t.Errorf("expected %q to be omitted when unset, got %q", k, graph.Attrs[k])
		}
	}
}

func TestFromDippinIR_WorkflowVarsMappedToParams(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "VarsTest",
		Start: "start",
		Exit:  "exit",
		Vars: map[string]string{
			"foo": "bar",
			"env": "prod",
		},
		Nodes: []*ir.Node{
			{ID: "start", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			{ID: "exit", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
		},
		Edges: []*ir.Edge{{From: "start", To: "exit"}},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}

	if graph.Attrs["params.foo"] != "bar" {
		t.Fatalf("params.foo = %q, want bar", graph.Attrs["params.foo"])
	}
	if graph.Attrs["params.env"] != "prod" {
		t.Fatalf("params.env = %q, want prod", graph.Attrs["params.env"])
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

// TestEnsureStartExitNodes_ShapeSetWithPrompt verifies that start/exit nodes
// with prompts still get their Mdiamond/Msquare shapes but keep their handler.
func TestEnsureStartExitNodes_ShapeSetWithPrompt(t *testing.T) {
	g := &Graph{
		Nodes:     make(map[string]*Node),
		StartNode: "begin",
		ExitNode:  "end",
	}
	g.Nodes["begin"] = &Node{ID: "begin", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "hello"}}
	g.Nodes["end"] = &Node{ID: "end", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "bye"}}
	g.Edges = []*Edge{{From: "begin", To: "end"}}

	err := ensureStartExitNodes(g)
	if err != nil {
		t.Fatalf("ensureStartExitNodes failed: %v", err)
	}

	if g.Nodes["begin"].Shape != "Mdiamond" {
		t.Errorf("begin shape = %q, want Mdiamond", g.Nodes["begin"].Shape)
	}
	if g.Nodes["begin"].Handler != "codergen" {
		t.Errorf("begin handler = %q, want codergen (preserved)", g.Nodes["begin"].Handler)
	}
	if g.Nodes["end"].Shape != "Msquare" {
		t.Errorf("end shape = %q, want Msquare", g.Nodes["end"].Shape)
	}
	if g.Nodes["end"].Handler != "codergen" {
		t.Errorf("end handler = %q, want codergen (preserved)", g.Nodes["end"].Handler)
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

func TestExtractAgentAttrs_LastResponseTruncate(t *testing.T) {
	attrs := make(map[string]string)
	extractAgentAttrs(ir.AgentConfig{LastResponseTruncate: 500}, attrs)
	if attrs["last_response_truncate"] != "500" {
		t.Errorf("last_response_truncate = %q, want 500", attrs["last_response_truncate"])
	}
}

func TestExtractAgentAttrs_ZeroLastResponseTruncateOmitted(t *testing.T) {
	attrs := make(map[string]string)
	extractAgentAttrs(ir.AgentConfig{}, attrs)
	if _, ok := attrs["last_response_truncate"]; ok {
		t.Error("last_response_truncate should be omitted when zero")
	}
}

func TestExtractParallelAttrs_BranchLastResponseTruncate(t *testing.T) {
	attrs := make(map[string]string)
	cfg := ir.ParallelConfig{
		Branches: []ir.BranchConfig{
			{Target: "NodeA", LastResponseTruncate: 200},
			{Target: "NodeB"},
		},
	}
	extractParallelAttrs(cfg, attrs)
	if attrs["branch.0.last_response_truncate"] != "200" {
		t.Errorf("branch.0.last_response_truncate = %q, want 200", attrs["branch.0.last_response_truncate"])
	}
	if _, ok := attrs["branch.1.last_response_truncate"]; ok {
		t.Error("branch.1.last_response_truncate should be omitted when zero")
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

// TestExtractToolAttrs_MarkerRouteOutputLimitForwarded asserts the three v0.28.0
// passthroughs (MarkerGrep, RouteRequired, OutputLimit) land in node attrs with the
// wire-contract names that dippin-lang's DOT exporter emits.
func TestExtractToolAttrs_MarkerRouteOutputLimitForwarded(t *testing.T) {
	attrs := map[string]string{}
	extractToolAttrs(ir.ToolConfig{
		MarkerGrep:    `^_TRACKER_ROUTE=`,
		RouteRequired: true,
		OutputLimit:   131072,
	}, attrs)

	if got, want := attrs["marker_grep"], `^_TRACKER_ROUTE=`; got != want {
		t.Errorf("marker_grep = %q, want %q", got, want)
	}
	if got, want := attrs["route_required"], "true"; got != want {
		t.Errorf("route_required = %q, want %q", got, want)
	}
	if got, want := attrs["output_limit"], "131072"; got != want {
		t.Errorf("output_limit = %q, want %q", got, want)
	}
}

// TestFromDippinIR_ToolConfigMarkerRouteOutputLimit asserts end-to-end forwarding
// through FromDippinIR — the path a real .dip file takes.
func TestFromDippinIR_ToolConfigMarkerRouteOutputLimit(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "ToolMarkerTest",
		Start: "start",
		Exit:  "tool",
		Nodes: []*ir.Node{
			{ID: "start", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			{
				ID:   "tool",
				Kind: ir.NodeTool,
				Config: ir.ToolConfig{
					Command:       "./run.sh",
					MarkerGrep:    `^STATUS=(ok|fail)$`,
					RouteRequired: true,
					OutputLimit:   65536,
				},
			},
		},
		Edges: []*ir.Edge{{From: "start", To: "tool"}},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}

	node := graph.Nodes["tool"]
	if got, want := node.Attrs["marker_grep"], `^STATUS=(ok|fail)$`; got != want {
		t.Errorf("marker_grep = %q, want %q", got, want)
	}
	if got, want := node.Attrs["route_required"], "true"; got != want {
		t.Errorf("route_required = %q, want %q", got, want)
	}
	if got, want := node.Attrs["output_limit"], "65536"; got != want {
		t.Errorf("output_limit = %q, want %q", got, want)
	}
}

// TestExtractToolAttrs_OutputsForwarded asserts the v0.29.0 `Outputs` field
// (closes dippin-lang#44) lands in node attrs as a comma-joined string using
// the wire-contract name dippin-lang's DOT exporter emits (`outputs`).
func TestExtractToolAttrs_OutputsForwarded(t *testing.T) {
	attrs := map[string]string{}
	extractToolAttrs(ir.ToolConfig{
		Outputs: []string{"pass", "fail", "retry"},
	}, attrs)

	if got, want := attrs["outputs"], "pass,fail,retry"; got != want {
		t.Errorf("outputs = %q, want %q", got, want)
	}
}

// TestFromDippinIR_ToolConfigOutputs asserts end-to-end forwarding through
// FromDippinIR — the path a real .dip file takes.
func TestFromDippinIR_ToolConfigOutputs(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "ToolOutputsTest",
		Start: "start",
		Exit:  "tool",
		Nodes: []*ir.Node{
			{ID: "start", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			{
				ID:   "tool",
				Kind: ir.NodeTool,
				Config: ir.ToolConfig{
					Command: "./check.sh",
					Outputs: []string{"green", "yellow", "red"},
				},
			},
		},
		Edges: []*ir.Edge{{From: "start", To: "tool"}},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}

	node := graph.Nodes["tool"]
	if got, want := node.Attrs["outputs"], "green,yellow,red"; got != want {
		t.Errorf("outputs = %q, want %q", got, want)
	}
}

func TestConvertEdge_WeightAndRestart(t *testing.T) {
	irEdge := &ir.Edge{From: "a", To: "b", Weight: 5, Restart: true}
	gEdge, err := convertEdge(irEdge)
	if err != nil {
		t.Fatalf("convertEdge returned error: %v", err)
	}
	if gEdge.Attrs["weight"] != "5" {
		t.Errorf("weight = %q, want %q", gEdge.Attrs["weight"], "5")
	}
	if gEdge.Attrs["restart"] != "true" {
		t.Errorf("restart = %q, want %q", gEdge.Attrs["restart"], "true")
	}
}

func TestConvertEdge_ZeroWeightOmitted(t *testing.T) {
	irEdge := &ir.Edge{From: "a", To: "b"}
	gEdge, err := convertEdge(irEdge)
	if err != nil {
		t.Fatalf("convertEdge returned error: %v", err)
	}
	if _, ok := gEdge.Attrs["weight"]; ok {
		t.Error("zero weight should not be in attrs")
	}
	if _, ok := gEdge.Attrs["restart"]; ok {
		t.Error("false restart should not be in attrs")
	}
}

func TestConvertEdge_Override(t *testing.T) {
	irEdge := &ir.Edge{From: "gate", To: "accept", Label: "approve", Override: true}
	gEdge, err := convertEdge(irEdge)
	if err != nil {
		t.Fatalf("convertEdge: %v", err)
	}
	if !gEdge.Override {
		t.Error("Override = false, want true")
	}
}

func TestConvertEdge_NoOverride(t *testing.T) {
	irEdge := &ir.Edge{From: "a", To: "b"}
	gEdge, err := convertEdge(irEdge)
	if err != nil {
		t.Fatalf("convertEdge: %v", err)
	}
	if gEdge.Override {
		t.Error("Override = true, want false for default edge")
	}
}

func TestAddIREdges_OverrideOnNonHumanNodeErrors(t *testing.T) {
	// override: true is only valid on edges from wait.human (hexagon) nodes.
	// The adapter must reject it on agent/tool/etc. source nodes.
	workflow := &ir.Workflow{
		Name:  "test",
		Start: "agent",
		Exit:  "done",
		Nodes: []*ir.Node{
			{ID: "agent", Kind: ir.NodeAgent, Config: ir.AgentConfig{Prompt: "do work"}},
			{ID: "done", Kind: ir.NodeAgent, Config: ir.AgentConfig{Prompt: "done"}},
		},
		Edges: []*ir.Edge{
			{From: "agent", To: "done", Override: true},
		},
	}
	_, err := FromDippinIR(workflow)
	if err == nil {
		t.Fatal("expected error for override: true on non-human edge, got nil")
	}
	if !errors.Is(err, ErrOverrideOnNonHumanEdge) {
		t.Errorf("expected ErrOverrideOnNonHumanEdge, got: %v", err)
	}
}

func TestAddIREdges_OverrideOnHumanNodeOK(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "test",
		Start: "gate",
		Exit:  "done",
		Nodes: []*ir.Node{
			{ID: "gate", Kind: ir.NodeHuman, Config: ir.HumanConfig{}},
			{ID: "done", Kind: ir.NodeAgent, Config: ir.AgentConfig{Prompt: "done"}},
		},
		Edges: []*ir.Edge{
			{From: "gate", To: "done", Label: "approve", Override: true},
		},
	}
	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("unexpected error for override: true on human gate: %v", err)
	}
	edges := graph.OutgoingEdges("gate")
	if len(edges) != 1 || !edges[0].Override {
		t.Error("expected Override=true on cloned edge")
	}
}

func TestConvertEdge_Choice(t *testing.T) {
	irEdge := &ir.Edge{From: "gate", To: "next", Label: "Approve and Continue", Choice: "approve"}
	gEdge, err := convertEdge(irEdge)
	if err != nil {
		t.Fatalf("convertEdge: %v", err)
	}
	if gEdge.Choice != "approve" {
		t.Errorf("Choice = %q, want %q", gEdge.Choice, "approve")
	}
	if gEdge.Label != "Approve and Continue" {
		t.Errorf("Label = %q, want %q", gEdge.Label, "Approve and Continue")
	}
}

func TestConvertEdge_NoChoice(t *testing.T) {
	irEdge := &ir.Edge{From: "a", To: "b", Label: "ok"}
	gEdge, err := convertEdge(irEdge)
	if err != nil {
		t.Fatalf("convertEdge: %v", err)
	}
	if gEdge.Choice != "" {
		t.Errorf("Choice = %q, want empty for edge without choice", gEdge.Choice)
	}
}

// TestEnsureStartExitNodes_ToolNodeKeepsHandler verifies that tool start/exit nodes
// with a tool_command attribute keep their "tool" handler and are not overwritten
// with the passthrough "start"/"exit" handler. This is the root cause of issue #69.
func TestEnsureStartExitNodes_ToolNodeKeepsHandler(t *testing.T) {
	// Simulate a tool Start node: shape=parallelogram (from adapter), handler=tool,
	// tool_command set (from ToolConfig.Command).
	g := &Graph{
		Nodes:     make(map[string]*Node),
		StartNode: "Start",
		ExitNode:  "Exit",
	}
	g.Nodes["Start"] = &Node{
		ID:      "Start",
		Shape:   "parallelogram",
		Handler: "tool",
		Attrs:   map[string]string{"tool_command": "touch start.txt"},
	}
	g.Nodes["Exit"] = &Node{
		ID:      "Exit",
		Shape:   "parallelogram",
		Handler: "tool",
		Attrs:   map[string]string{"tool_command": "touch exit.txt"},
	}
	g.Edges = []*Edge{{From: "Start", To: "Exit"}}

	err := ensureStartExitNodes(g)
	if err != nil {
		t.Fatalf("ensureStartExitNodes failed: %v", err)
	}

	// Shapes must be overridden to Mdiamond/Msquare for validator.
	if g.Nodes["Start"].Shape != "Mdiamond" {
		t.Errorf("start shape = %q, want Mdiamond", g.Nodes["Start"].Shape)
	}
	if g.Nodes["Exit"].Shape != "Msquare" {
		t.Errorf("exit shape = %q, want Msquare", g.Nodes["Exit"].Shape)
	}

	// Handlers must NOT be overwritten — tool commands need the tool handler.
	if g.Nodes["Start"].Handler != "tool" {
		t.Errorf("start handler = %q, want tool (preserved)", g.Nodes["Start"].Handler)
	}
	if g.Nodes["Exit"].Handler != "tool" {
		t.Errorf("exit handler = %q, want tool (preserved)", g.Nodes["Exit"].Handler)
	}
}

// TestEnsureStartExitNodes_HumanNodeKeepsHandler verifies that human start/exit
// nodes with a mode attribute keep their "wait.human" handler.
func TestEnsureStartExitNodes_HumanNodeKeepsHandler(t *testing.T) {
	g := &Graph{
		Nodes:     make(map[string]*Node),
		StartNode: "Begin",
		ExitNode:  "Finish",
	}
	g.Nodes["Begin"] = &Node{
		ID:      "Begin",
		Shape:   "hexagon",
		Handler: "wait.human",
		Attrs:   map[string]string{"mode": "yes_no"},
	}
	g.Nodes["Finish"] = &Node{
		ID:      "Finish",
		Shape:   "hexagon",
		Handler: "wait.human",
		Attrs:   map[string]string{"mode": "yes_no"},
	}
	g.Edges = []*Edge{{From: "Begin", To: "Finish"}}

	err := ensureStartExitNodes(g)
	if err != nil {
		t.Fatalf("ensureStartExitNodes failed: %v", err)
	}

	if g.Nodes["Begin"].Handler != "wait.human" {
		t.Errorf("start handler = %q, want wait.human (preserved)", g.Nodes["Begin"].Handler)
	}
	if g.Nodes["Finish"].Handler != "wait.human" {
		t.Errorf("exit handler = %q, want wait.human (preserved)", g.Nodes["Finish"].Handler)
	}
}

// TestFromDippinIR_ToolStartExitNodes verifies that tool nodes used as start/exit
// retain the "tool" handler when converted via FromDippinIR. This is the full
// adapter-level regression test for issue #69.
func TestFromDippinIR_ToolStartExitNodes(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "ToolStartExitTest",
		Start: "Start",
		Exit:  "Exit",
		Nodes: []*ir.Node{
			{
				ID:     "Start",
				Kind:   ir.NodeTool,
				Label:  "Start",
				Config: ir.ToolConfig{Command: "touch start.txt"},
			},
			{
				ID:     "Middle",
				Kind:   ir.NodeTool,
				Label:  "Middle",
				Config: ir.ToolConfig{Command: "touch middle.txt"},
			},
			{
				ID:     "Exit",
				Kind:   ir.NodeTool,
				Label:  "Exit",
				Config: ir.ToolConfig{Command: "touch exit.txt"},
			},
		},
		Edges: []*ir.Edge{
			{From: "Start", To: "Middle"},
			{From: "Middle", To: "Exit"},
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}

	// All three nodes must use the "tool" handler.
	for _, id := range []string{"Start", "Middle", "Exit"} {
		node := graph.Nodes[id]
		if node == nil {
			t.Fatalf("node %q not found in graph", id)
		}
		if node.Handler != "tool" {
			t.Errorf("node %q handler = %q, want tool", id, node.Handler)
		}
	}

	// Start/exit nodes get their shapes set for the validator.
	if graph.Nodes["Start"].Shape != "Mdiamond" {
		t.Errorf("Start shape = %q, want Mdiamond", graph.Nodes["Start"].Shape)
	}
	if graph.Nodes["Exit"].Shape != "Msquare" {
		t.Errorf("Exit shape = %q, want Msquare", graph.Nodes["Exit"].Shape)
	}

	// Tool commands must be preserved in attrs.
	if graph.Nodes["Start"].Attrs["tool_command"] != "touch start.txt" {
		t.Errorf("Start tool_command = %q, want touch start.txt", graph.Nodes["Start"].Attrs["tool_command"])
	}
	if graph.Nodes["Exit"].Attrs["tool_command"] != "touch exit.txt" {
		t.Errorf("Exit tool_command = %q, want touch exit.txt", graph.Nodes["Exit"].Attrs["tool_command"])
	}
}

// TestNodeHasHandlerContent verifies the helper that distinguishes bare nodes
// from nodes with handler-specific content. Two cases are treated as bare
// passthroughs: codergen nodes with no prompt and nodes with an empty/unresolved
// Handler field. Any other handler type is considered meaningful and must be
// preserved.
func TestNodeHasHandlerContent(t *testing.T) {
	tests := []struct {
		name string
		node *Node
		want bool
	}{
		{
			name: "bare codergen node - no content",
			node: &Node{ID: "n", Handler: "codergen", Attrs: make(map[string]string)},
			want: false,
		},
		{
			name: "agent node with prompt",
			node: &Node{ID: "n", Handler: "codergen", Attrs: map[string]string{"prompt": "do something"}},
			want: true,
		},
		{
			name: "tool node with command",
			node: &Node{ID: "n", Handler: "tool", Attrs: map[string]string{"tool_command": "echo hi"}},
			want: true,
		},
		{
			name: "human node with mode",
			node: &Node{ID: "n", Handler: "wait.human", Attrs: map[string]string{"mode": "yes_no"}},
			want: true,
		},
		{
			name: "codergen node with only non-content attrs",
			node: &Node{ID: "n", Handler: "codergen", Attrs: map[string]string{"label": "My Node", "llm_model": "claude"}},
			want: false,
		},
		// Handler-type regression cases — all non-codergen handlers must be preserved.
		{
			name: "wait.human without mode attr (default mode)",
			node: &Node{ID: "n", Handler: "wait.human", Attrs: make(map[string]string)},
			want: true,
		},
		{
			name: "parallel node with parallel_targets",
			node: &Node{ID: "n", Handler: "parallel", Attrs: map[string]string{"parallel_targets": "A,B"}},
			want: true,
		},
		{
			name: "parallel.fan_in node with fan_in_sources",
			node: &Node{ID: "n", Handler: "parallel.fan_in", Attrs: map[string]string{"fan_in_sources": "A,B"}},
			want: true,
		},
		{
			name: "conditional node (no attrs, pure routing)",
			node: &Node{ID: "n", Handler: "conditional", Attrs: make(map[string]string)},
			want: true,
		},
		{
			name: "subgraph node with ref",
			node: &Node{ID: "n", Handler: "subgraph", Attrs: map[string]string{"subgraph_ref": "sub.dip"}},
			want: true,
		},
		{
			name: "stack.manager_loop node",
			node: &Node{ID: "n", Handler: "stack.manager_loop", Attrs: make(map[string]string)},
			want: true,
		},
		{
			name: "unresolved handler - empty string, no prompt",
			node: &Node{ID: "n", Handler: "", Attrs: make(map[string]string)},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nodeHasHandlerContent(tt.node)
			if got != tt.want {
				t.Errorf("nodeHasHandlerContent(%v) = %v, want %v", tt.node.Attrs, got, tt.want)
			}
		})
	}
}

// TestEnsureStartExitNodes_ParamsLeakPrevention is a regression test for the
// AgentConfig.Params "mode"/"tool_command" collision bug (Codex P2, PR #72 / issue #69).
//
// A user-defined param named "mode" or "tool_command" leaked into node attrs via
// extractAgentAttrs. The old attr-based nodeHasHandlerContent saw those keys and
// falsely concluded the node was a human or tool node — preserving the codergen
// handler even though the node had no prompt, which caused a runtime failure.
//
// With the handler-aware check: codergen + no prompt → nodeHasHandlerContent returns
// false → ensureStartExitNodes correctly assigns the passthrough start/exit handler.
func TestEnsureStartExitNodes_ParamsLeakPrevention(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "ParamsLeakTest",
		Start: "Begin",
		Exit:  "End",
		Nodes: []*ir.Node{
			{
				ID:   "Begin",
				Kind: ir.NodeAgent,
				Config: ir.AgentConfig{
					// No prompt — bare passthrough agent node. Params["mode"] and
					// Params["tool_command"] will be copied into node attrs by
					// extractAgentAttrs. Under the old attr-based check these keys
					// caused a false positive; the handler-aware check must ignore them.
					Params: map[string]string{"mode": "something_custom", "tool_command": "echo collide"},
				},
			},
			{
				ID:   "End",
				Kind: ir.NodeAgent,
				Config: ir.AgentConfig{
					Params: map[string]string{"mode": "something_custom"},
				},
			},
		},
		Edges: []*ir.Edge{
			{From: "Begin", To: "End"},
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}

	begin := graph.Nodes["Begin"]
	if begin == nil {
		t.Fatal("Begin node not found")
	}
	// Leaked Params attrs ("mode", "tool_command") must NOT prevent the start handler
	// from being assigned. The node has no prompt, so it is a passthrough start node.
	if begin.Handler != "start" {
		t.Errorf("Begin handler = %q, want start; leaked Params attrs should not prevent passthrough assignment", begin.Handler)
	}
	if begin.Shape != "Mdiamond" {
		t.Errorf("Begin shape = %q, want Mdiamond", begin.Shape)
	}
	// Confirm the leaked attrs are present in node attrs (so the test exercises the
	// exact collision path).
	if begin.Attrs["mode"] != "something_custom" {
		t.Errorf("Begin attrs[\"mode\"] = %q, want something_custom (leak not reproduced)", begin.Attrs["mode"])
	}

	end := graph.Nodes["End"]
	if end == nil {
		t.Fatal("End node not found")
	}
	if end.Handler != "exit" {
		t.Errorf("End handler = %q, want exit; leaked Params attrs should not prevent passthrough assignment", end.Handler)
	}
	if end.Shape != "Msquare" {
		t.Errorf("End shape = %q, want Msquare", end.Shape)
	}
}

// TestEnsureStartExitNodes_ParallelStartExit verifies that a parallel node
// designated as the workflow start keeps its "parallel" handler and is not
// overwritten with the passthrough "start" sentinel. Drives through FromDippinIR.
func TestEnsureStartExitNodes_ParallelStartExit(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "ParallelStartTest",
		Start: "Dispatch",
		Exit:  "Join",
		Nodes: []*ir.Node{
			{
				ID:    "Dispatch",
				Kind:  ir.NodeParallel,
				Label: "Dispatch",
				Config: ir.ParallelConfig{
					Targets: []string{"BranchA", "BranchB"},
				},
			},
			{ID: "BranchA", Kind: ir.NodeAgent, Config: ir.AgentConfig{Prompt: "Branch A work"}},
			{ID: "BranchB", Kind: ir.NodeAgent, Config: ir.AgentConfig{Prompt: "Branch B work"}},
			{
				ID:    "Join",
				Kind:  ir.NodeFanIn,
				Label: "Join",
				Config: ir.FanInConfig{
					Sources: []string{"BranchA", "BranchB"},
				},
			},
		},
		Edges: []*ir.Edge{},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}

	dispatch := graph.Nodes["Dispatch"]
	if dispatch == nil {
		t.Fatal("Dispatch node not found")
	}
	if dispatch.Handler != "parallel" {
		t.Errorf("Dispatch handler = %q, want parallel (must be preserved)", dispatch.Handler)
	}
	if dispatch.Shape != "Mdiamond" {
		t.Errorf("Dispatch shape = %q, want Mdiamond", dispatch.Shape)
	}

	join := graph.Nodes["Join"]
	if join == nil {
		t.Fatal("Join node not found")
	}
	if join.Handler != "parallel.fan_in" {
		t.Errorf("Join handler = %q, want parallel.fan_in (must be preserved)", join.Handler)
	}
	if join.Shape != "Msquare" {
		t.Errorf("Join shape = %q, want Msquare", join.Shape)
	}
}

// TestEnsureStartExitNodes_ConditionalStartExit verifies that a conditional node
// used as start/exit retains its "conditional" handler after FromDippinIR.
func TestEnsureStartExitNodes_ConditionalStartExit(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "ConditionalStartTest",
		Start: "Route",
		Exit:  "Done",
		Nodes: []*ir.Node{
			{ID: "Route", Kind: ir.NodeConditional, Label: "Route"},
			{ID: "Done", Kind: ir.NodeConditional, Label: "Done"},
		},
		Edges: []*ir.Edge{
			{From: "Route", To: "Done"},
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}

	route := graph.Nodes["Route"]
	if route == nil {
		t.Fatal("Route node not found")
	}
	if route.Handler != "conditional" {
		t.Errorf("Route handler = %q, want conditional (must be preserved)", route.Handler)
	}
	if route.Shape != "Mdiamond" {
		t.Errorf("Route shape = %q, want Mdiamond", route.Shape)
	}

	done := graph.Nodes["Done"]
	if done == nil {
		t.Fatal("Done node not found")
	}
	if done.Handler != "conditional" {
		t.Errorf("Done handler = %q, want conditional (must be preserved)", done.Handler)
	}
	if done.Shape != "Msquare" {
		t.Errorf("Done shape = %q, want Msquare", done.Shape)
	}
}

// TestEnsureStartExitNodes_SubgraphStartExit verifies that a subgraph node
// used as start/exit retains its "subgraph" handler after FromDippinIR.
func TestEnsureStartExitNodes_SubgraphStartExit(t *testing.T) {
	workflow := &ir.Workflow{
		Name:  "SubgraphStartTest",
		Start: "Sub",
		Exit:  "Cleanup",
		Nodes: []*ir.Node{
			{
				ID:     "Sub",
				Kind:   ir.NodeSubgraph,
				Label:  "Sub",
				Config: ir.SubgraphConfig{Ref: "child.dip"},
			},
			{
				ID:     "Cleanup",
				Kind:   ir.NodeSubgraph,
				Label:  "Cleanup",
				Config: ir.SubgraphConfig{Ref: "cleanup.dip"},
			},
		},
		Edges: []*ir.Edge{
			{From: "Sub", To: "Cleanup"},
		},
	}

	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR failed: %v", err)
	}

	sub := graph.Nodes["Sub"]
	if sub == nil {
		t.Fatal("Sub node not found")
	}
	if sub.Handler != "subgraph" {
		t.Errorf("Sub handler = %q, want subgraph (must be preserved)", sub.Handler)
	}
	if sub.Shape != "Mdiamond" {
		t.Errorf("Sub shape = %q, want Mdiamond", sub.Shape)
	}

	cleanup := graph.Nodes["Cleanup"]
	if cleanup == nil {
		t.Fatal("Cleanup node not found")
	}
	if cleanup.Handler != "subgraph" {
		t.Errorf("Cleanup handler = %q, want subgraph (must be preserved)", cleanup.Handler)
	}
	if cleanup.Shape != "Msquare" {
		t.Errorf("Cleanup shape = %q, want Msquare", cleanup.Shape)
	}
}

// TestFromDippinIR_BackendAttrRoundTrip is the end-to-end regression test for
// issue #91: backend: claude-code on an agent node must land in
// graph.Nodes[id].Attrs["backend"]. This test drives the full
// parser → IR → adapter pipeline to catch silent drops at any layer.
func TestFromDippinIR_BackendAttrRoundTrip(t *testing.T) {
	src := `workflow BackendTest
  goal: "Test per-node backend"
  start: Start
  exit: Native

  defaults
    model: "gpt-4.1-mini"
    provider: openai

  tool Start
    label: "Setup"
    timeout: 5s
    command:
      echo start

  agent Claude
    backend: claude-code
    prompt: Hello from Claude

  agent Native
    backend: native
    model: "deepseek-v3"
    prompt: Hello from native

  edges
    Start -> Claude
    Claude -> Native
`
	p := parser.NewParser(src, "backend_test.dip")
	wf, err := p.Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Verify the IR has the Backend field populated.
	for _, n := range wf.Nodes {
		cfg, ok := n.Config.(ir.AgentConfig)
		if !ok {
			continue
		}
		switch n.ID {
		case "Claude":
			if cfg.Backend != "claude-code" {
				t.Errorf("IR node Claude: Backend = %q, want %q", cfg.Backend, "claude-code")
			}
		case "Native":
			if cfg.Backend != "native" {
				t.Errorf("IR node Native: Backend = %q, want %q", cfg.Backend, "native")
			}
		}
	}

	// Convert through the adapter and verify graph attrs.
	graph, err := FromDippinIR(wf)
	if err != nil {
		t.Fatalf("FromDippinIR: %v", err)
	}

	claude := graph.Nodes["Claude"]
	if claude == nil {
		t.Fatal("graph missing node Claude")
	}
	if claude.Attrs["backend"] != "claude-code" {
		t.Errorf("graph Claude backend = %q, want %q", claude.Attrs["backend"], "claude-code")
	}

	native := graph.Nodes["Native"]
	if native == nil {
		t.Fatal("graph missing node Native")
	}
	if native.Attrs["backend"] != "native" {
		t.Errorf("graph Native backend = %q, want %q", native.Attrs["backend"], "native")
	}
	if native.Attrs["llm_model"] != "deepseek-v3" {
		t.Errorf("graph Native model = %q, want %q", native.Attrs["llm_model"], "deepseek-v3")
	}
}

// TestFromDippinIR_RequiresPopulatesGraphAttr verifies that workflow.Requires
// (dippin-lang v0.26.0+) flows through the adapter to graph.Attrs["requires"]
// and is parseable by Graph.RequiredDeps(). Regression guard for the v0.29.0
// git preflight integration.
func TestFromDippinIR_RequiresPopulatesGraphAttr(t *testing.T) {
	wf := &ir.Workflow{
		Name:     "TestRequires",
		Start:    "Start",
		Exit:     "Exit",
		Requires: []string{"git", "docker"},
		Nodes: []*ir.Node{
			{ID: "Start", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			{ID: "Exit", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
		},
		Edges: []*ir.Edge{{From: "Start", To: "Exit"}},
	}
	g, err := FromDippinIR(wf)
	if err != nil {
		t.Fatal(err)
	}
	if got := g.Attrs["requires"]; got != "git, docker" {
		t.Errorf("requires attr: want %q, got %q", "git, docker", got)
	}
	deps := g.RequiredDeps()
	if len(deps) != 2 || deps[0] != "git" || deps[1] != "docker" {
		t.Errorf("RequiredDeps: want [git docker], got %v", deps)
	}
}

func TestFromDippinIR_RequiresEmpty(t *testing.T) {
	wf := &ir.Workflow{
		Name:  "TestNoRequires",
		Start: "Start",
		Exit:  "Exit",
		Nodes: []*ir.Node{
			{ID: "Start", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			{ID: "Exit", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
		},
		Edges: []*ir.Edge{{From: "Start", To: "Exit"}},
	}
	g, err := FromDippinIR(wf)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := g.Attrs["requires"]; ok {
		t.Errorf("expected no 'requires' attr when workflow.Requires is empty")
	}
}

func TestFromDippinIR_RequiresTrimsWhitespaceAndDropsEmpty(t *testing.T) {
	wf := &ir.Workflow{
		Name:     "TestRequiresMessy",
		Start:    "Start",
		Exit:     "Exit",
		Requires: []string{"  git  ", "", "docker"},
		Nodes: []*ir.Node{
			{ID: "Start", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			{ID: "Exit", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
		},
		Edges: []*ir.Edge{{From: "Start", To: "Exit"}},
	}
	g, err := FromDippinIR(wf)
	if err != nil {
		t.Fatal(err)
	}
	if got := g.Attrs["requires"]; got != "git, docker" {
		t.Errorf("requires attr: want %q (trimmed, empty dropped), got %q", "git, docker", got)
	}
}

// TestFromDippinIR_RequiresDeduplicates verifies the adapter removes
// duplicates while preserving declaration order. PR #235 review.
func TestFromDippinIR_RequiresDeduplicates(t *testing.T) {
	wf := &ir.Workflow{
		Name:     "TestRequiresDuplicates",
		Start:    "Start",
		Exit:     "Exit",
		Requires: []string{"git", "docker", "git", "docker", "jq"},
		Nodes: []*ir.Node{
			{ID: "Start", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			{ID: "Exit", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
		},
		Edges: []*ir.Edge{{From: "Start", To: "Exit"}},
	}
	g, err := FromDippinIR(wf)
	if err != nil {
		t.Fatal(err)
	}
	if got := g.Attrs["requires"]; got != "git, docker, jq" {
		t.Errorf("requires attr: want %q (deduplicated), got %q", "git, docker, jq", got)
	}
	deps := g.RequiredDeps()
	want := []string{"git", "docker", "jq"}
	if len(deps) != len(want) {
		t.Fatalf("RequiredDeps: want %v, got %v", want, deps)
	}
	for i := range want {
		if deps[i] != want[i] {
			t.Errorf("idx %d: want %q, got %q", i, want[i], deps[i])
		}
	}
}

func TestExtractAgentAttrs_WritablePaths(t *testing.T) {
	t.Run("typed field populates attrs", func(t *testing.T) {
		cfg := ir.AgentConfig{
			WritablePaths: []string{"workspace/**", ".ai/sprints/**"},
		}
		attrs := map[string]string{}
		extractAgentAttrs(cfg, attrs)
		got, ok := attrs["writable_paths"]
		if !ok {
			t.Fatal("attrs missing writable_paths key")
		}
		if got != "workspace/**,.ai/sprints/**" {
			t.Errorf("attrs[writable_paths] = %q, want %q", got, "workspace/**,.ai/sprints/**")
		}
	})

	t.Run("Params writable_paths cannot override typed field", func(t *testing.T) {
		cfg := ir.AgentConfig{
			WritablePaths: []string{"workspace/**"},
			Params:        map[string]string{"writable_paths": "/etc/**"},
		}
		attrs := map[string]string{}
		extractAgentAttrs(cfg, attrs)
		if attrs["writable_paths"] != "workspace/**" {
			t.Errorf("Params override won (got %q); typed field should have won (want %q)",
				attrs["writable_paths"], "workspace/**")
		}
	})

	t.Run("Params writable_paths is blocked when typed empty (bypass defense)", func(t *testing.T) {
		// SECURITY: a workflow author putting writable_paths in params: is
		// either confused or attempting bypass. Either way, we refuse to
		// let Params reach the attrs map. We instead claim the key with an
		// empty value — Task 14's configureJail will fail-CLOSED on this,
		// not silently accept the attacker's globs.
		cfg := ir.AgentConfig{
			WritablePaths: nil,
			Params:        map[string]string{"writable_paths": "/etc/**"},
		}
		attrs := map[string]string{}
		extractAgentAttrs(cfg, attrs)
		got, ok := attrs["writable_paths"]
		if !ok {
			t.Fatal("attrs missing writable_paths key; bypass defense did not claim it")
		}
		if got != "" {
			t.Errorf("attrs[writable_paths] = %q, want %q (empty signals fail-CLOSED to Task 14)", got, "")
		}
	})

	t.Run("typed empty + no params = no attr", func(t *testing.T) {
		// When writable_paths is genuinely absent (no typed field, no params
		// entry), the attr is absent — the accessor returns WritablePathsSet
		// = false and Task 14 leaves the jail disabled.
		cfg := ir.AgentConfig{
			WritablePaths: nil,
			Params:        map[string]string{"other_key": "ok"},
		}
		attrs := map[string]string{}
		extractAgentAttrs(cfg, attrs)
		if _, ok := attrs["writable_paths"]; ok {
			t.Errorf("attrs has writable_paths key = %q, want absent", attrs["writable_paths"])
		}
		// Sanity: the unrelated Params key did spill.
		if attrs["other_key"] != "ok" {
			t.Errorf("Params spill for unrelated key failed: %v", attrs)
		}
	})
}

func TestExtractAgentAttrs_ToolAccess(t *testing.T) {
	t.Run("typed field populates attrs", func(t *testing.T) {
		// Issue #366: top-level `tool_access: none` is the documented form;
		// the adapter must copy the typed IR field, not just Params.
		cfg := ir.AgentConfig{
			ToolAccess: "none",
		}
		attrs := map[string]string{}
		extractAgentAttrs(cfg, attrs)
		got, ok := attrs["tool_access"]
		if !ok {
			t.Fatal("attrs missing tool_access key")
		}
		if got != "none" {
			t.Errorf("attrs[tool_access] = %q, want %q", got, "none")
		}
	})

	t.Run("typed field wins over conflicting Params", func(t *testing.T) {
		cfg := ir.AgentConfig{
			ToolAccess: "none",
			Params:     map[string]string{"tool_access": "full"},
		}
		attrs := map[string]string{}
		extractAgentAttrs(cfg, attrs)
		if attrs["tool_access"] != "none" {
			t.Errorf("Params override won (got %q); typed field should have won (want %q)",
				attrs["tool_access"], "none")
		}
	})

	t.Run("Params form still works when typed empty", func(t *testing.T) {
		// Back-compat: the params: spelling enforced before #366 and must
		// keep enforcing after.
		cfg := ir.AgentConfig{
			Params: map[string]string{"tool_access": "none"},
		}
		attrs := map[string]string{}
		extractAgentAttrs(cfg, attrs)
		if attrs["tool_access"] != "none" {
			t.Errorf("attrs[tool_access] = %q, want %q", attrs["tool_access"], "none")
		}
	})

	t.Run("whitespace-only typed field = no attr", func(t *testing.T) {
		// Whitespace-only is semantically unset:
		// agent.SessionConfig.IsToolAccessRestricted trims before checking,
		// so " " would read as unrestricted anyway. Don't claim the attr.
		cfg := ir.AgentConfig{
			ToolAccess: "   ",
		}
		attrs := map[string]string{}
		extractAgentAttrs(cfg, attrs)
		if _, ok := attrs["tool_access"]; ok {
			t.Errorf("attrs has tool_access key = %q, want absent", attrs["tool_access"])
		}
	})

	t.Run("typed empty + no params = no attr", func(t *testing.T) {
		cfg := ir.AgentConfig{}
		attrs := map[string]string{}
		extractAgentAttrs(cfg, attrs)
		if _, ok := attrs["tool_access"]; ok {
			t.Errorf("attrs has tool_access key = %q, want absent", attrs["tool_access"])
		}
	})
}

func TestExtractParallelAttrs_BranchSecurityOverrides(t *testing.T) {
	t.Run("tool_access and writable_paths serialize as branch attrs", func(t *testing.T) {
		// Issue #368: per-branch security overrides were silently dropped.
		// writable_paths uses the SAME comma-join encoding as the agent-level
		// attr so AgentConfig parses branch values identically.
		cfg := ir.ParallelConfig{
			Branches: []ir.BranchConfig{
				{Target: "A", ToolAccess: "none", WritablePaths: []string{"src/**", "docs/*.md"}},
				{Target: "B"},
			},
		}
		attrs := map[string]string{}
		extractParallelAttrs(cfg, attrs)
		if got := attrs["branch.0.tool_access"]; got != "none" {
			t.Errorf("branch.0.tool_access = %q, want none", got)
		}
		if got := attrs["branch.0.writable_paths"]; got != "src/**,docs/*.md" {
			t.Errorf("branch.0.writable_paths = %q, want src/**,docs/*.md", got)
		}
	})

	t.Run("empty values inherit — no attr written", func(t *testing.T) {
		// Per IR doc comments: empty INHERITS the target agent's value,
		// never resets to full catalog / unbounded. Absent attr = the
		// cloned branch node keeps the agent's own attr.
		cfg := ir.ParallelConfig{
			Branches: []ir.BranchConfig{
				{Target: "A"},
			},
		}
		attrs := map[string]string{}
		extractParallelAttrs(cfg, attrs)
		if v, ok := attrs["branch.0.tool_access"]; ok {
			t.Errorf("branch.0.tool_access present = %q, want absent (inherit)", v)
		}
		if v, ok := attrs["branch.0.writable_paths"]; ok {
			t.Errorf("branch.0.writable_paths present = %q, want absent (inherit)", v)
		}
	})

	t.Run("whitespace-only tool_access = no attr", func(t *testing.T) {
		// Mirrors the agent-level rule (#366): whitespace-only is
		// semantically unset; don't claim the attr.
		cfg := ir.ParallelConfig{
			Branches: []ir.BranchConfig{
				{Target: "A", ToolAccess: "   "},
			},
		}
		attrs := map[string]string{}
		extractParallelAttrs(cfg, attrs)
		if v, ok := attrs["branch.0.tool_access"]; ok {
			t.Errorf("branch.0.tool_access present = %q, want absent", v)
		}
	})
}
