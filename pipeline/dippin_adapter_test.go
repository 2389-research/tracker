// ABOUTME: Comprehensive tests for the Dippin IR to Graph adapter.
// ABOUTME: Validates field mappings, node kind conversions, and round-trip fidelity.
package pipeline

import (
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
		{"model", "claude-3.5-sonnet"},
		{"provider", "anthropic"},
		{"max_turns", "5"},
		{"cmd_timeout", "30s"},
		{"cache_tools", "true"},
		{"compaction", "aggressive"},
		{"compaction_threshold", "0.75"},
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
	if node.Attrs["default"] != "yes" {
		t.Errorf("default = %q, want %q", node.Attrs["default"], "yes")
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

	// Params are serialized as comma-separated, order may vary
	params := node.Attrs["subgraph_params"]
	if !contains(params, "env=prod") || !contains(params, "timeout=30s") {
		t.Errorf("subgraph_params = %q, want to contain env=prod and timeout=30s", params)
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
		{"default_model", "gpt-4"},
		{"default_provider", "openai"},
		{"default_retry_policy", "standard"},
		{"default_max_retries", "3"},
		{"default_fidelity", "strict"},
		{"max_restarts", "10"},
		{"restart_target", "start"},
		{"cache_tools", "true"},
		{"default_compaction", "conservative"},
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
		{"fallback_target", "exit"},
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
			if !contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// Helper function to check if a string contains a substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
