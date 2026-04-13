// ABOUTME: Variable interpolation for ${namespace.key} syntax in prompts and attributes.
// ABOUTME: Supports three namespaces: ctx (pipeline context), params (subgraph parameters), graph (graph attributes).
package pipeline

import (
	"fmt"
	"strings"
)

// toolCommandSafeCtxKeys lists the only ctx.* keys allowed in tool_command
// variable expansion. All other ctx.* keys are blocked to prevent LLM output
// injection into shell commands.
var toolCommandSafeCtxKeys = map[string]bool{
	"outcome":           true,
	"preferred_label":   true,
	"human_response":    true,
	"interview_answers": true,
}

// ExpandVariables replaces ${namespace.key} patterns with values from the provided sources.
// Supports three namespaces:
//   - ctx: runtime context (from PipelineContext)
//   - params: subgraph parameters (passed explicitly)
//   - graph: graph-level attributes (from Graph.Attrs)
//
// In lenient mode (strict=false), undefined variables expand to empty string.
// In strict mode (strict=true), undefined variables return an error.
//
// When toolCommandMode is true (optional variadic parameter), only allowlisted
// ctx.* keys can be expanded — all others return an error to prevent LLM output
// injection into shell commands.
//
// Examples:
//
//	${ctx.human_response} → value from PipelineContext
//	${params.model} → value from subgraph params
//	${graph.goal} → value from graph attributes
func ExpandVariables(
	text string,
	ctx *PipelineContext,
	params map[string]string,
	graphAttrs map[string]string,
	strict bool,
	toolCommandMode ...bool,
) (string, error) {
	if text == "" {
		return text, nil
	}
	tcMode := len(toolCommandMode) > 0 && toolCommandMode[0]
	return expandVariablesPass(text, ctx, params, graphAttrs, strict, tcMode)
}

// expandVariablesPass performs the single-pass variable expansion scan.
func expandVariablesPass(
	text string,
	ctx *PipelineContext,
	params map[string]string,
	graphAttrs map[string]string,
	strict bool,
	tcMode bool,
) (string, error) {
	var buf strings.Builder
	buf.Grow(len(text))
	pos := 0
	for pos < len(text) {
		startIdx := strings.Index(text[pos:], "${")
		if startIdx == -1 {
			buf.WriteString(text[pos:])
			break
		}
		startIdx += pos
		buf.WriteString(text[pos:startIdx])

		endIdx := strings.Index(text[startIdx+2:], "}")
		if endIdx == -1 {
			buf.WriteString(text[startIdx:])
			pos = len(text)
			break
		}
		endIdx += startIdx + 2

		varExpr := text[startIdx+2 : endIdx]
		parts := strings.SplitN(varExpr, ".", 2)
		if varExpr == "" || len(parts) != 2 {
			buf.WriteString(text[startIdx : endIdx+1])
			pos = endIdx + 1
			continue
		}

		value, err := resolveVariableValue(parts[0], parts[1], ctx, params, graphAttrs, strict, tcMode)
		if err != nil {
			return "", err
		}
		buf.WriteString(value)
		pos = endIdx + 1
	}
	return buf.String(), nil
}

// resolveVariableValue looks up a variable and applies tool-command safety and strict-mode checks.
// Returns the resolved value (empty string for undefined in lenient mode) or an error.
func resolveVariableValue(
	namespace, key string,
	ctx *PipelineContext,
	params map[string]string,
	graphAttrs map[string]string,
	strict, toolCommandMode bool,
) (string, error) {
	value, found, err := lookupVariable(namespace, key, ctx, params, graphAttrs)
	if err != nil {
		return "", err
	}

	if toolCommandMode && found && namespace == "ctx" && !toolCommandSafeCtxKeys[key] {
		return "", fmt.Errorf(
			"tool_command references unsafe variable ${ctx.%s} — "+
				"LLM/tool output cannot be interpolated into shell commands. "+
				"Safe ctx keys: outcome, preferred_label, human_response, interview_answers. "+
				"Write output to a file in a prior tool node and read it in your command instead",
			key,
		)
	}

	if !found {
		if strict {
			available := availableKeys(namespace, ctx, params, graphAttrs)
			return "", fmt.Errorf(
				"undefined variable ${%s.%s} (available keys in %s: %v)",
				namespace, key, namespace, available,
			)
		}
		return "", nil
	}
	return value, nil
}

// lookupVariable retrieves a value from the appropriate namespace.
// Returns (value, found, error).
func lookupVariable(
	namespace, key string,
	ctx *PipelineContext,
	params map[string]string,
	graphAttrs map[string]string,
) (string, bool, error) {
	switch namespace {
	case "ctx":
		if ctx == nil {
			return "", false, nil
		}
		val, ok := ctx.Get(key)
		return val, ok, nil

	case "params":
		if params == nil {
			return "", false, nil
		}
		val, ok := params[key]
		return val, ok, nil

	case "graph":
		if graphAttrs == nil {
			return "", false, nil
		}
		val, ok := graphAttrs[key]
		return val, ok, nil

	default:
		// Unknown namespace - return as not found (lenient) or error (strict handled by caller)
		return "", false, nil
	}
}

// availableKeys returns a list of available keys in the given namespace for error messages.
func availableKeys(
	namespace string,
	ctx *PipelineContext,
	params map[string]string,
	graphAttrs map[string]string,
) []string {
	keys := keysForNamespace(namespace, ctx, params, graphAttrs)
	if len(keys) == 0 {
		return []string{"(none)"}
	}
	return keys
}

// keysForNamespace extracts map keys for the given variable namespace.
func keysForNamespace(namespace string, ctx *PipelineContext, params, graphAttrs map[string]string) []string {
	switch namespace {
	case "ctx":
		return snapshotKeys(ctx)
	case "params":
		return mapKeys(params)
	case "graph":
		return mapKeys(graphAttrs)
	}
	return nil
}

func snapshotKeys(ctx *PipelineContext) []string {
	if ctx == nil {
		return nil
	}
	snapshot := ctx.Snapshot()
	keys := make([]string, 0, len(snapshot))
	for k := range snapshot {
		keys = append(keys, k)
	}
	return keys
}

func mapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// ParseSubgraphParams parses a comma-separated string of key=value pairs into a map.
// Format: "key1=val1,key2=val2"
// Returns an empty map if the input is empty.
func ParseSubgraphParams(paramsStr string) map[string]string {
	result := make(map[string]string)
	if paramsStr == "" {
		return result
	}

	pairs := strings.Split(paramsStr, ",")
	for _, pair := range pairs {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			if key != "" {
				result[key] = val
			}
		}
	}

	return result
}

// InjectParamsIntoGraph creates a new graph with variable expansion applied to all
// node attributes. This is used by the subgraph handler to inject params before execution.
func InjectParamsIntoGraph(g *Graph, params map[string]string) (*Graph, error) {
	clone := &Graph{
		Name:      g.Name,
		Nodes:     make(map[string]*Node, len(g.Nodes)),
		Edges:     make([]*Edge, 0, len(g.Edges)),
		Attrs:     copyStringMap(g.Attrs),
		NodeOrder: append([]string(nil), g.NodeOrder...),
		StartNode: g.StartNode,
		ExitNode:  g.ExitNode,
	}

	for id, node := range g.Nodes {
		cloned, err := cloneNodeWithExpansion(id, node, params, g.Attrs)
		if err != nil {
			return nil, err
		}
		clone.Nodes[id] = cloned
	}

	for _, e := range g.Edges {
		clone.Edges = append(clone.Edges, &Edge{
			From:      e.From,
			To:        e.To,
			Label:     e.Label,
			Condition: e.Condition,
			Attrs:     copyStringMap(e.Attrs),
		})
	}

	return clone, nil
}

// cloneNodeWithExpansion clones a node and expands param variables in its attributes.
func cloneNodeWithExpansion(id string, node *Node, params, graphAttrs map[string]string) (*Node, error) {
	cloned := &Node{
		ID:      node.ID,
		Shape:   node.Shape,
		Label:   node.Label,
		Handler: node.Handler,
		Attrs:   make(map[string]string, len(node.Attrs)),
	}

	for key, val := range node.Attrs {
		expanded, err := ExpandVariables(val, nil, params, graphAttrs, false)
		if err != nil {
			return nil, fmt.Errorf("failed to expand variable in node %s attr %s: %w", id, key, err)
		}
		cloned.Attrs[key] = expanded
	}

	if node.Label != "" {
		expanded, err := ExpandVariables(node.Label, nil, params, graphAttrs, false)
		if err != nil {
			return nil, fmt.Errorf("failed to expand variable in node %s label: %w", id, err)
		}
		cloned.Label = expanded
	}

	return cloned, nil
}

func copyStringMap(m map[string]string) map[string]string {
	c := make(map[string]string, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}
