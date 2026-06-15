package pipeline

import (
	"strings"
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

func TestExpandVariables_ToolCommandMode_BlocksLLMOutput(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("last_response", "malicious; rm -rf /")
	ctx.Set("outcome", "success")

	_, err := ExpandVariables("echo ${ctx.last_response}", ctx, nil, nil, false, true)
	if err == nil {
		t.Fatal("expected error for tainted key in tool command mode")
	}
	if !strings.Contains(err.Error(), "unsafe variable") {
		t.Errorf("error = %q, want 'unsafe variable' message", err)
	}

	result, err := ExpandVariables("status=${ctx.outcome}", ctx, nil, nil, false, true)
	if err != nil {
		t.Fatalf("unexpected error for safe key: %v", err)
	}
	if result != "status=success" {
		t.Errorf("result = %q, want %q", result, "status=success")
	}
}

func TestExpandVariables_ToolCommandMode_AllowsHumanResponse(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("human_response", "user typed this")

	result, err := ExpandVariables("echo ${ctx.human_response}", ctx, nil, nil, false, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "echo user typed this" {
		t.Errorf("result = %q, want %q", result, "echo user typed this")
	}
}

func TestExpandVariables_ToolCommandMode_BlocksResponsePrefix(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("response.agent1", "LLM output here")

	_, err := ExpandVariables("echo ${ctx.response.agent1}", ctx, nil, nil, false, true)
	if err == nil {
		t.Fatal("expected error for response.* key in tool command mode")
	}
}

func TestExpandVariables_ToolCommandMode_AllowsGraphAndParams(t *testing.T) {
	ctx := NewPipelineContext()
	graphAttrs := map[string]string{"goal": "build the app"}
	params := map[string]string{"model": "sonnet"}

	result, err := ExpandVariables("${graph.goal} ${params.model}", ctx, params, graphAttrs, false, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "build the app sonnet" {
		t.Errorf("result = %q, want %q", result, "build the app sonnet")
	}
}

func TestExpandVariables_ToolCommandMode_WorkflowDir(t *testing.T) {
	ctx := NewPipelineContext()

	// Seeded: ${graph.workflow_dir} expands in tool_command mode (graph.* is
	// author/operator-controlled, on the allowlist).
	graphAttrs := map[string]string{"workflow_dir": "/abs/path/to/dev_loop"}
	result, err := ExpandVariables("bash ${graph.workflow_dir}/scripts/setup.sh", ctx, nil, graphAttrs, false, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "bash /abs/path/to/dev_loop/scripts/setup.sh" {
		t.Errorf("result = %q, want %q", result, "bash /abs/path/to/dev_loop/scripts/setup.sh")
	}

	// Not seeded (embedded built-in, .dipx, library source): lenient mode
	// expands the absent key to empty string — pin that semantic.
	result, err = ExpandVariables("bash ${graph.workflow_dir}/scripts/setup.sh", ctx, nil, map[string]string{}, false, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "bash /scripts/setup.sh" {
		t.Errorf("absent workflow_dir: result = %q, want %q", result, "bash /scripts/setup.sh")
	}
}

func TestExpandVariables_NormalMode_AllowsEverything(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("last_response", "hello world")

	result, err := ExpandVariables("echo ${ctx.last_response}", ctx, nil, nil, false, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "echo hello world" {
		t.Errorf("result = %q, want %q", result, "echo hello world")
	}
}

func TestExpandVariables_ToolStdoutFencedBlock(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("tool_stdout", "line one\nline two\nline three")
	ctx.Set("tool_stderr", "error: something went wrong")
	ctx.Set("outcome", "fail")
	ctx.Set("human_response", "yes please")

	tests := []struct {
		name            string
		input           string
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:  "tool_stdout renders as fenced block",
			input: "The output was: ${ctx.tool_stdout} and we should proceed.",
			wantContains: []string{
				"## Tool Stdout",
				"```text",
				"line one\nline two\nline three",
				"```",
			},
			wantNotContains: []string{
				"The output was: line one",
			},
		},
		{
			name:  "tool_stderr renders as fenced block",
			input: "Errors: ${ctx.tool_stderr} done.",
			wantContains: []string{
				"## Tool Stderr",
				"```text",
				"error: something went wrong",
				"```",
			},
			wantNotContains: []string{
				"Errors: error: something went wrong done.",
			},
		},
		{
			name:  "outcome still expands inline",
			input: "Result: ${ctx.outcome} end.",
			wantContains: []string{
				"Result: fail end.",
			},
			wantNotContains: []string{
				"```text",
				"## Tool",
			},
		},
		{
			name:  "human_response still expands inline",
			input: "User said: ${ctx.human_response}.",
			wantContains: []string{
				"User said: yes please.",
			},
			wantNotContains: []string{
				"```text",
				"## Tool",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ExpandVariables(tt.input, ctx, nil, nil, false)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("result does not contain %q\ngot: %q", want, result)
				}
			}
			for _, notWant := range tt.wantNotContains {
				if strings.Contains(result, notWant) {
					t.Errorf("result unexpectedly contains %q\ngot: %q", notWant, result)
				}
			}
		})
	}
}

func TestExpandVariables_ToolStdoutEmpty(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("tool_stdout", "")

	result, err := ExpandVariables("Before: ${ctx.tool_stdout} After.", ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Empty tool_stdout should not produce a spurious fenced block — it expands
	// to empty string like any other undefined/empty ctx key.
	if strings.Contains(result, "```text") {
		t.Errorf("empty tool_stdout produced spurious fenced block: %q", result)
	}
	want := "Before:  After."
	if result != want {
		t.Errorf("expected %q for empty tool_stdout, got %q", want, result)
	}
}

func TestExpandVariables_NodeScopedToolStdoutFencedBlock(t *testing.T) {
	// Node-scoped keys like ${ctx.node.RunTests.tool_stdout} must also render
	// as fenced blocks — not inline. Codex finding P2: the bare-key switch only
	// matched "tool_stdout"; per-node references used key="node.RunTests.tool_stdout"
	// and bypassed the fenced rendering.
	ctx := NewPipelineContext()
	ctx.Set("node.RunTests.tool_stdout", "line one\nline two")
	ctx.Set("node.RunTests.tool_stderr", "warn: something")

	tests := []struct {
		name         string
		input        string
		wantContains []string
	}{
		{
			name:  "node-scoped tool_stdout renders as fenced block",
			input: "Output: ${ctx.node.RunTests.tool_stdout} done.",
			wantContains: []string{
				"## Tool Stdout",
				"```text",
				"line one\nline two",
				"```",
			},
		},
		{
			name:  "node-scoped tool_stderr renders as fenced block",
			input: "Errors: ${ctx.node.RunTests.tool_stderr} done.",
			wantContains: []string{
				"## Tool Stderr",
				"```text",
				"warn: something",
				"```",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ExpandVariables(tt.input, ctx, nil, nil, false)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("result does not contain %q\ngot: %q", want, result)
				}
			}
		})
	}
}

func TestInjectParamsIntoGraphPreservesValidationAndIndexes(t *testing.T) {
	g := NewGraph("sub")
	g.AddNode(&Node{ID: "a", Shape: "box"})
	g.AddNode(&Node{ID: "b", Shape: "box"})
	g.AddEdge(&Edge{From: "a", To: "b"})
	g.DippinValidated = true

	clone, err := InjectParamsIntoGraph(g, map[string]string{})
	if err != nil {
		t.Fatalf("InjectParamsIntoGraph: %v", err)
	}
	if !clone.DippinValidated {
		t.Error("clone lost DippinValidated flag")
	}
	if len(clone.outgoing["a"]) != 1 || len(clone.incoming["b"]) != 1 {
		t.Errorf("clone adjacency indexes not populated: outgoing[a]=%d incoming[b]=%d",
			len(clone.outgoing["a"]), len(clone.incoming["b"]))
	}
	if len(clone.Edges) != 1 {
		t.Fatalf("expected 1 cloned edge, got %d", len(clone.Edges))
	}
	if clone.Edges[0] == g.Edges[0] {
		t.Error("clone shares edge pointers with source graph")
	}
}
