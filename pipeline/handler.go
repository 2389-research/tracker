// ABOUTME: Handler interface and registry for pipeline node execution dispatch.
// ABOUTME: Each node shape maps to a handler; the registry resolves and executes them.
package pipeline

import (
	"context"
	"fmt"
)

const (
	OutcomeSuccess TerminalStatus = "success"
	OutcomeRetry   TerminalStatus = "retry"
	OutcomeFail    TerminalStatus = "fail"
)

// Outcome represents the result of executing a handler on a pipeline node.
type Outcome struct {
	Status             string
	ContextUpdates     map[string]string
	PreferredLabel     string
	SuggestedNextNodes []string
	Stats              *SessionStats // optional, populated by codergen handler
	// ChildUsage is the aggregated usage of a child run that executed under
	// this node (subgraph, manager_loop). When non-nil, Trace.AggregateUsage
	// folds it into totals and per-provider rollups so the parent trace
	// reflects spend that happened inside the child. Required for
	// BudgetGuard enforcement to see child spend once control returns to
	// the parent.
	ChildUsage *UsageSummary
	// Truncations records output streams that exceeded their per-stream
	// cap during this node's execution. The engine emits one
	// EventToolOutputTruncated per entry so `tracker diagnose` and the
	// audit log can correlate routing misses with truncation (issue #208).
	// Currently populated only by the tool handler.
	Truncations []TruncationDetail
	// MissingMarker records a marker_grep regex that produced no match
	// (issue #210). When non-nil the engine emits EventToolMarkerMissing
	// before the engine's normal post-node accounting; the handler also
	// sets Status = OutcomeFail so routing does not silently fall through
	// to an unconditional edge. Populated only by the tool handler.
	MissingMarker *MarkerDetail
	// MissingRoute records the absence of a _TRACKER_ROUTE= sentinel
	// in captured stdout when the node had route_required: true
	// (#212). Same flow as MissingMarker: handler sets Status = Fail,
	// engine emits EventToolRouteMissing. Sentinel extraction itself
	// runs unconditionally; this field is populated only when the
	// missing-sentinel + route_required combination triggers a fail.
	MissingRoute *RouteDetail
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

// Has reports whether a handler with the given name is registered.
func (r *HandlerRegistry) Has(name string) bool {
	_, ok := r.handlers[name]
	return ok
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
