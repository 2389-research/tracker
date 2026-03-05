// ABOUTME: Tool interface and Registry for agent tool dispatch.
// ABOUTME: Tools register by name, export LLM tool definitions, and execute via ToolCallData.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/2389-research/mammoth-lite/llm"
)

// Tool defines the interface that all agent tools must implement.
type Tool interface {
	Name() string
	Description() string
	Parameters() json.RawMessage
	Execute(ctx context.Context, input json.RawMessage) (string, error)
}

// Registry holds registered tools and dispatches execution requests.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register adds a tool to the registry, keyed by its name.
func (r *Registry) Register(tool Tool) {
	r.tools[tool.Name()] = tool
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
			Content:    fmt.Sprintf("error: %s", err.Error()),
			IsError:    true,
		}
	}

	return llm.ToolResultData{
		ToolCallID: call.ID,
		Name:       call.Name,
		Content:    output,
		IsError:    false,
	}
}
