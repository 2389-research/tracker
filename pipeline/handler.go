// ABOUTME: Handler interface and registry for pipeline node execution dispatch.
// ABOUTME: Each node shape maps to a handler; the registry resolves and executes them.
package pipeline

import (
	"context"
	"fmt"
)

const (
	OutcomeSuccess = "success"
	OutcomeRetry   = "retry"
	OutcomeFail    = "fail"
)

// Outcome represents the result of executing a handler on a pipeline node.
type Outcome struct {
	Status             string
	ContextUpdates     map[string]string
	PreferredLabel     string
	SuggestedNextNodes []string
}

// Handler defines the interface for pipeline node execution. Each handler has
// a unique name and an Execute method that processes a node within a pipeline context.
type Handler interface {
	Name() string
	Execute(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error)
}

// HandlerRegistry stores named handlers and dispatches execution to the
// appropriate handler based on a node's Handler field.
type HandlerRegistry struct {
	handlers map[string]Handler
}

// NewHandlerRegistry creates an empty handler registry.
func NewHandlerRegistry() *HandlerRegistry {
	return &HandlerRegistry{handlers: make(map[string]Handler)}
}

// Register adds a handler to the registry, keyed by its Name(). If a handler
// with the same name already exists, it is overwritten.
func (r *HandlerRegistry) Register(h Handler) {
	r.handlers[h.Name()] = h
}

// Get returns the handler registered under the given name, or nil if not found.
func (r *HandlerRegistry) Get(name string) Handler {
	return r.handlers[name]
}

// Execute looks up the handler for the given node and delegates execution to it.
// Returns an error if no handler is registered for the node's Handler field.
func (r *HandlerRegistry) Execute(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
	h := r.Get(node.Handler)
	if h == nil {
		return Outcome{}, fmt.Errorf("no handler registered for %q (node %q)", node.Handler, node.ID)
	}
	return h.Execute(ctx, node, pctx)
}
