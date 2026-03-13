// ABOUTME: Tool interface and Registry for agent tool dispatch.
// ABOUTME: Tools register by name, export LLM tool definitions, and execute via ToolCallData.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/2389-research/tracker/llm"
)

// Tool defines the interface that all agent tools must implement.
type Tool interface {
	Name() string
	Description() string
	Parameters() json.RawMessage
	Execute(ctx context.Context, input json.RawMessage) (string, error)
}

// CachePolicy declares how a tool's results may be cached.
type CachePolicy int

const (
	// CachePolicyNone means the tool has no caching opinion (default).
	CachePolicyNone CachePolicy = iota
	// CachePolicyCacheable means results can be cached for identical inputs.
	CachePolicyCacheable
	// CachePolicyMutating means the tool modifies state and invalidates caches.
	CachePolicyMutating
)

// CachePolicyProvider is an optional interface tools can implement to declare caching behavior.
type CachePolicyProvider interface {
	CachePolicy() CachePolicy
}

// GetCachePolicy returns the CachePolicy for a tool. If the tool implements
// CachePolicyProvider, its declared policy is returned; otherwise CachePolicyNone.
func GetCachePolicy(t Tool) CachePolicy {
	if cp, ok := t.(CachePolicyProvider); ok {
		return cp.CachePolicy()
	}
	return CachePolicyNone
}

// Registry holds registered tools and dispatches execution requests.
type Registry struct {
	tools        map[string]Tool
	outputLimits map[string]int
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools:        make(map[string]Tool),
		outputLimits: make(map[string]int),
	}
}

// Register adds a tool to the registry, keyed by its name.
func (r *Registry) Register(tool Tool) {
	r.tools[tool.Name()] = tool
}

// SetOutputLimits configures per-tool output truncation limits.
func (r *Registry) SetOutputLimits(limits map[string]int) {
	clear(r.outputLimits)
	for name, limit := range limits {
		if limit > 0 {
			r.outputLimits[name] = limit
		}
	}
}

// Get returns the tool with the given name, or nil if not found.
func (r *Registry) Get(name string) Tool {
	return r.tools[name]
}

// Definitions returns LLM-compatible tool definitions for all registered tools,
// sorted alphabetically by name for deterministic ordering.
func (r *Registry) Definitions() []llm.ToolDefinition {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	defs := make([]llm.ToolDefinition, 0, len(r.tools))
	for _, name := range names {
		t := r.tools[name]
		defs = append(defs, llm.ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		})
	}
	return defs
}

// Execute dispatches a tool call to the appropriate registered tool and returns
// the result. Returns an error result if the tool is not found or execution fails.
func (r *Registry) Execute(ctx context.Context, call llm.ToolCallData) llm.ToolResultData {
	tool := r.Get(call.Name)
	if tool == nil {
		return llm.ToolResultData{
			ToolCallID: call.ID,
			Name:       call.Name,
			Content:    fmt.Sprintf("error: unknown tool %q", call.Name),
			IsError:    true,
		}
	}

	output, err := tool.Execute(ctx, call.Arguments)
	if err != nil {
		return llm.ToolResultData{
			ToolCallID: call.ID,
			Name:       call.Name,
			Content:    fmt.Sprintf("Tool error (%s): %s", call.Name, err.Error()),
			IsError:    true,
		}
	}

	return llm.ToolResultData{
		ToolCallID: call.ID,
		Name:       call.Name,
		Content:    truncateOutput(output, r.outputLimit(call.Name)),
		IsError:    false,
	}
}

func (r *Registry) outputLimit(toolName string) int {
	if limit, ok := r.outputLimits[toolName]; ok && limit > 0 {
		return limit
	}
	return defaultToolOutputLimit(toolName)
}

func defaultToolOutputLimit(toolName string) int {
	switch toolName {
	case "read":
		return 50000
	case "bash":
		return 30000
	case "grep_search":
		return 20000
	case "edit":
		return 10000
	case "apply_patch":
		return 10000
	case "write":
		return 1000
	default:
		return maxToolOutputLen
	}
}
