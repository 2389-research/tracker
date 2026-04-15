// ABOUTME: Thread-safe key-value store shared across all pipeline nodes during execution.
// ABOUTME: Provides Get/Set/Merge/Snapshot operations and separate internal state for engine bookkeeping.
// ABOUTME: Supports per-node namespace scoping via ScopeToNode — dirty keys are copied into node.<id>.<key>
// ABOUTME: after each node completes, preserving individual node outputs without breaking global backward compat.
package pipeline

import (
	"fmt"
	"strings"
	"sync"
)

// Built-in context keys used by the engine and handlers.
const (
	ContextKeyOutcome            = "outcome"
	ContextKeyPreferredLabel     = "preferred_label"
	ContextKeyGoal               = "graph.goal"
	ContextKeyLastResponse       = "last_response"
	ContextKeyHumanResponse      = "human_response"
	ContextKeyToolStdout         = "tool_stdout"
	ContextKeyToolStderr         = "tool_stderr"
	ContextKeySuggestedNextNodes = "suggested_next_nodes"

	// ContextKeyResponsePrefix is prepended to a node ID to form a per-node
	// response key (e.g. "response.mynode"). Downstream nodes can reference
	// specific upstream outputs without relying on last_response being current.
	ContextKeyResponsePrefix = "response."

	// ContextKeyTurnLimitMsg holds a diagnostic message when an agent exhausts
	// its turn limit or enters a tool call loop. Present only in failure outcomes
	// from turn-limit exhaustion; absent on normal success.
	ContextKeyTurnLimitMsg = "turn_limit_msg"

	// Interview mode context keys. Overridable via questions_key/answers_key
	// node attributes in .dip files. These are the defaults when the attrs
	// are not specified.
	ContextKeyInterviewQuestions = "interview_questions"
	ContextKeyInterviewAnswers   = "interview_answers"

	// ContextKeyNodePrefix is the prefix for per-node scoped context keys.
	// After each node completes, ScopeToNode copies dirty keys into this namespace
	// so downstream nodes can read e.g. "node.MyAgent.last_response" without
	// colliding with the global "last_response" written by later nodes.
	ContextKeyNodePrefix = "node."
)

// Internal context keys used by the engine for bookkeeping.
const (
	InternalKeyArtifactDir = "_artifact_dir"
)

// PipelineContext is a thread-safe key-value store shared across all pipeline
// nodes during execution. It has two namespaces: user-visible values and
// internal engine bookkeeping (retry counters, loop state).
//
// Per-node scoping: every key written via Set or Merge is recorded in a dirty
// set. After a node's handler completes, the engine calls ScopeToNode(nodeID)
// to copy those dirty keys into "node.<nodeID>.<key>" entries. The dirty set is
// then cleared, ready for the next node. Bare keys continue to be overwritten
// globally (last-writer-wins), preserving full backward compatibility.
type PipelineContext struct {
	mu       sync.RWMutex
	values   map[string]string
	internal map[string]string
	dirty    map[string]struct{}
}

// NewPipelineContext creates an empty pipeline context.
func NewPipelineContext() *PipelineContext {
	return &PipelineContext{
		values:   make(map[string]string),
		internal: make(map[string]string),
		dirty:    make(map[string]struct{}),
	}
}

// Get retrieves a value from the user-visible context.
func (c *PipelineContext) Get(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.values[key]
	return v, ok
}

// Set stores a value in the user-visible context and marks the key as dirty
// so it will be included in the next ScopeToNode call.
func (c *PipelineContext) Set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values[key] = value
	c.dirty[key] = struct{}{}
}

// Merge applies all key-value pairs from updates into the user-visible context
// and marks each key as dirty so it will be included in the next ScopeToNode call.
func (c *PipelineContext) Merge(updates map[string]string) {
	if updates == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range updates {
		c.values[k] = v
		c.dirty[k] = struct{}{}
	}
}

// Snapshot returns a shallow copy of the user-visible context values.
func (c *PipelineContext) Snapshot() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	snap := make(map[string]string, len(c.values))
	for k, v := range c.values {
		snap[k] = v
	}
	return snap
}

// GetInternal retrieves a value from the internal engine namespace.
func (c *PipelineContext) GetInternal(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.internal[key]
	return v, ok
}

// SetInternal stores a value in the internal engine namespace.
func (c *PipelineContext) SetInternal(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.internal[key] = value
}

// DiffFrom returns all keys in the current context whose values differ from
// the given baseline snapshot, including keys that exist in the context but
// not in baseline. Keys present in baseline but absent here are not reported
// (PipelineContext has no delete operation).
func (c *PipelineContext) DiffFrom(baseline map[string]string) map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	diff := make(map[string]string)
	for k, v := range c.values {
		if baseVal, exists := baseline[k]; !exists || baseVal != v {
			diff[k] = v
		}
	}
	return diff
}

// ScopeToNode copies all dirty (recently-written) keys into the per-node
// namespace "node.<nodeID>.<key>" and then clears the dirty set. This lets
// downstream nodes read a specific upstream node's output — for example
// "node.MyAgent.last_response" — without being affected by later writes to
// the bare "last_response" key. The bare keys are NOT removed; they retain
// their last-writer-wins global semantics for backward compatibility.
//
// Keys that already start with ContextKeyNodePrefix (e.g. "node.X.foo") are
// skipped — scoping them would create confusing doubly-nested keys like
// "node.<id>.node.X.foo". The engine passes the node's graph ID directly.
func (c *PipelineContext) ScopeToNode(nodeID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := fmt.Sprintf("%s%s.", ContextKeyNodePrefix, nodeID)
	for k := range c.dirty {
		if strings.HasPrefix(k, ContextKeyNodePrefix) {
			continue
		}
		c.values[prefix+k] = c.values[k]
	}
	c.dirty = make(map[string]struct{})
}

// ClearDirty resets the dirty set without scoping any keys. Call this after
// all bootstrap writes (graph attrs, initial context, checkpoint restore) are
// done and before the main engine loop starts, so that baseline values are not
// copied into the first node's scoped namespace.
func (c *PipelineContext) ClearDirty() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dirty = make(map[string]struct{})
}

// GetScoped retrieves the value of key from the per-node namespace for nodeID.
// It is equivalent to Get("node.<nodeID>.<key>") but more readable at call
// sites. Returns ("", false) if the scoped key has not been written.
func (c *PipelineContext) GetScoped(nodeID, key string) (string, bool) {
	return c.Get(fmt.Sprintf("%s%s.%s", ContextKeyNodePrefix, nodeID, key))
}

// NewPipelineContextFrom creates a PipelineContext pre-populated with the
// given values. Used by the parallel handler to give each branch an isolated
// snapshot of the shared context.
//
// Preloaded values are written directly without marking them dirty, so the
// first ScopeToNode call after construction only scopes keys that were written
// after construction — not the entire baseline snapshot.
func NewPipelineContextFrom(values map[string]string) *PipelineContext {
	ctx := NewPipelineContext()
	for k, v := range values {
		ctx.values[k] = v
	}
	return ctx
}
