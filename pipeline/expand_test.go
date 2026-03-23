package pipeline

import (
	"testing"
)

func TestExpandVariables_CtxNamespace(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("outcome", "success")
	ctx.Set("last_response", "All tests passed")

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "single ctx variable",
			input:    "Status: ${ctx.outcome}",
			expected: "Status: success",
		},
		{
			name:     "multiple ctx variables",
			input:    "Outcome: ${ctx.outcome}, Response: ${ctx.last_response}",
			expected: "Outcome: success, Response: All tests passed",
		},
		{
			name:     "ctx variable at start",
			input:    "${ctx.outcome} is the result",
			expected: "success is the result",
		},
		{
			name:     "ctx variable at end",
			input:    "Result is ${ctx.outcome}",
			expected: "Result is success",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ExpandVariables(tt.input, ctx, nil, nil, false)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestExpandVariables_ParamsNamespace(t *testing.T) {
	params := map[string]string{
		"model":    "gpt-4",
		"severity": "critical",
		"task":     "code review",
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "single param variable",
			input:    "Model: ${params.model}",
			expected: "Model: gpt-4",
		},
		{
			name:     "multiple param variables",
			input:    "Use ${params.model} for ${params.task}",
			expected: "Use gpt-4 for code review",
		},
		{
			name:     "param in sentence",
			input:    "Scan for ${params.severity} vulnerabilities.",
			expected: "Scan for critical vulnerabilities.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ExpandVariables(tt.input, nil, params, nil, false)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestExpandVariables_GraphNamespace(t *testing.T) {
	graphAttrs := map[string]string{
		"goal": "Build a secure authentication system",
		"name": "SecurityWorkflow",
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "graph goal variable",
			input:    "Our goal: ${graph.goal}",
			expected: "Our goal: Build a secure authentication system",
		},
		{
			name:     "graph name variable",
			input:    "Running workflow: ${graph.name}",
			expected: "Running workflow: SecurityWorkflow",
		},
		{
			name:     "multiple graph variables",
			input:    "${graph.name}: ${graph.goal}",
			expected: "SecurityWorkflow: Build a secure authentication system",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ExpandVariables(tt.input, nil, nil, graphAttrs, false)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestExpandVariables_MultipleNamespaces(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("human_response", "build app")

	params := map[string]string{
		"model": "gpt-4",
	}

	graphAttrs := map[string]string{
		"goal": "Create software",
	}

	input := "User: ${ctx.human_response}, Model: ${params.model}, Goal: ${graph.goal}"
	expected := "User: build app, Model: gpt-4, Goal: Create software"

	result, err := ExpandVariables(input, ctx, params, graphAttrs, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestExpandVariables_UndefinedLenient(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("known", "value")

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "undefined ctx variable",
			input:    "Value: ${ctx.unknown}",
			expected: "Value: ",
		},
		{
			name:     "undefined params variable",
			input:    "Param: ${params.missing}",
			expected: "Param: ",
		},
		{
			name:     "mix of defined and undefined",
			input:    "Known: ${ctx.known}, Unknown: ${ctx.unknown}",
			expected: "Known: value, Unknown: ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ExpandVariables(tt.input, ctx, nil, nil, false)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestExpandVariables_UndefinedStrict(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("known", "value")

	tests := []struct {
		name        string
		input       string
		shouldError bool
	}{
		{
			name:        "undefined ctx variable",
			input:       "Value: ${ctx.unknown}",
			shouldError: true,
		},
		{
			name:        "undefined params variable",
			input:       "Param: ${params.missing}",
			shouldError: true,
		},
		{
			name:        "defined variable",
			input:       "Known: ${ctx.known}",
			shouldError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ExpandVariables(tt.input, ctx, nil, nil, true)
			if tt.shouldError && err == nil {
				t.Error("expected error but got none")
			}
			if !tt.shouldError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestExpandVariables_NoVariables(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("key", "value")

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "plain text",
			input: "No variables here",
		},
		{
			name:  "text with dollar signs",
			input: "Cost: $100",
		},
		{
			name:  "text with braces",
			input: "Code: if (x > 0) { return true; }",
		},
		{
			name:  "empty string",
			input: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ExpandVariables(tt.input, ctx, nil, nil, false)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != tt.input {
				t.Errorf("got %q, want %q", result, tt.input)
			}
		})
	}
}

func TestExpandVariables_MalformedSyntax(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("key", "value")

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "missing closing brace",
			input:    "Value: ${ctx.key",
			expected: "Value: ${ctx.key",
		},
		{
			name:     "empty variable",
			input:    "Value: ${}",
			expected: "Value: ${}",
		},
		{
			name:     "no dot separator",
			input:    "Value: ${ctxkey}",
			expected: "Value: ${ctxkey}",
		},
		{
			name:     "unknown namespace",
			input:    "Value: ${unknown.key}",
			expected: "Value: ",
		},
		{
			name:     "malformed then valid",
			input:    "Bad: ${ctxkey} then Good: ${ctx.key}",
			expected: "Bad: ${ctxkey} then Good: value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ExpandVariables(tt.input, ctx, nil, nil, false)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestExpandVariables_ConsecutiveVariables(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("a", "A")
	ctx.Set("b", "B")
	ctx.Set("c", "C")

	input := "${ctx.a}${ctx.b}${ctx.c}"
	expected := "ABC"

	result, err := ExpandVariables(input, ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestExpandVariables_NoRecursiveExpansion(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("user_input", "value is ${ctx.secret}")
	ctx.Set("secret", "SHOULD_NOT_APPEAR")

	result, err := ExpandVariables("Input: ${ctx.user_input}", ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "Input: value is ${ctx.secret}"
	if result != expected {
		t.Errorf("got %q, want %q (no recursive expansion)", result, expected)
	}
}

func TestExpandVariables_NilInputs(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "nil context",
			input:    "${ctx.key}",
			expected: "",
		},
		{
			name:     "nil params",
			input:    "${params.key}",
			expected: "",
		},
		{
			name:     "nil graph attrs",
			input:    "${graph.key}",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ExpandVariables(tt.input, nil, nil, nil, false)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("got %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestParseSubgraphParams(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected map[string]string
	}{
		{
			name:  "single param",
			input: "model=gpt-4",
			expected: map[string]string{
				"model": "gpt-4",
			},
		},
		{
			name:  "multiple params",
			input: "model=gpt-4,severity=critical,task=review",
			expected: map[string]string{
				"model":    "gpt-4",
				"severity": "critical",
				"task":     "review",
			},
		},
		{
			name:  "params with spaces",
			input: "model = gpt-4 , severity = critical",
			expected: map[string]string{
				"model":    "gpt-4",
				"severity": "critical",
			},
		},
		{
			name:     "empty string",
			input:    "",
			expected: map[string]string{},
		},
		{
			name:  "param with equals in value",
			input: "formula=x=y+z",
			expected: map[string]string{
				"formula": "x=y+z",
			},
		},
		{
			name:     "malformed param (no equals)",
			input:    "model,severity=critical",
			expected: map[string]string{"severity": "critical"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseSubgraphParams(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("got %d params, want %d", len(result), len(tt.expected))
			}
			for k, v := range tt.expected {
				if result[k] != v {
					t.Errorf("param %q: got %q, want %q", k, result[k], v)
				}
			}
		})
	}
}

func TestInjectParamsIntoGraph(t *testing.T) {
	// Create a test graph with variables in attributes
	graph := NewGraph("TestGraph")
	graph.Attrs = map[string]string{
		"goal": "Test goal",
	}

	node := &Node{
		ID:      "Agent1",
		Shape:   "box",
		Label:   "Agent ${params.name}",
		Handler: "codergen",
		Attrs: map[string]string{
			"prompt":        "Use model ${params.model} for ${params.task}",
			"system_prompt": "You are ${params.role}",
			"llm_model":     "${params.model}",
		},
	}
	graph.AddNode(node)

	params := map[string]string{
		"name":  "Analyzer",
		"model": "gpt-4",
		"task":  "code review",
		"role":  "a code reviewer",
	}

	// Inject params
	result, err := InjectParamsIntoGraph(graph, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify original graph unchanged
	if graph.Nodes["Agent1"].Label != "Agent ${params.name}" {
		t.Error("original graph was modified")
	}

	// Verify expanded graph
	expandedNode := result.Nodes["Agent1"]
	if expandedNode.Label != "Agent Analyzer" {
		t.Errorf("label: got %q, want %q", expandedNode.Label, "Agent Analyzer")
	}
	if expandedNode.Attrs["prompt"] != "Use model gpt-4 for code review" {
		t.Errorf("prompt: got %q", expandedNode.Attrs["prompt"])
	}
	if expandedNode.Attrs["system_prompt"] != "You are a code reviewer" {
		t.Errorf("system_prompt: got %q", expandedNode.Attrs["system_prompt"])
	}
	if expandedNode.Attrs["llm_model"] != "gpt-4" {
		t.Errorf("llm_model: got %q", expandedNode.Attrs["llm_model"])
	}
}

func TestInjectParamsIntoGraph_EmptyParams(t *testing.T) {
	graph := NewGraph("TestGraph")
	node := &Node{
		ID:      "Agent1",
		Handler: "codergen",
		Attrs: map[string]string{
			"prompt": "No variables here",
		},
	}
	graph.AddNode(node)

	result, err := InjectParamsIntoGraph(graph, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Nodes["Agent1"].Attrs["prompt"] != "No variables here" {
		t.Error("prompt was unexpectedly modified")
	}
}

func TestInjectParamsIntoGraph_MixedVariables(t *testing.T) {
	graph := NewGraph("TestGraph")
	graph.Attrs = map[string]string{
		"goal": "Test workflow",
	}

	node := &Node{
		ID:      "Agent1",
		Handler: "codergen",
		Attrs: map[string]string{
			"prompt": "Goal: ${graph.goal}, Model: ${params.model}",
		},
	}
	graph.AddNode(node)

	params := map[string]string{
		"model": "gpt-4",
	}

	result, err := InjectParamsIntoGraph(graph, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "Goal: Test workflow, Model: gpt-4"
	if result.Nodes["Agent1"].Attrs["prompt"] != expected {
		t.Errorf("got %q, want %q", result.Nodes["Agent1"].Attrs["prompt"], expected)
	}
}
