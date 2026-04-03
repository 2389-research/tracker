// ABOUTME: Thread-safe key-value store shared across all pipeline nodes during execution.
// ABOUTME: Provides Get/Set/Merge/Snapshot operations and separate internal state for engine bookkeeping.
package pipeline

import (
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

	// Interview mode context keys. Overridable via questions_key/answers_key
	// node attributes in .dip files. These are the defaults when the attrs
	// are not specified.
	ContextKeyInterviewQuestions = "interview_questions"
	ContextKeyInterviewAnswers   = "interview_answers"
)

// Internal context keys used by the engine for bookkeeping.
const (
	InternalKeyArtifactDir = "_artifact_dir"
)

// PipelineContext is a thread-safe key-value store shared across all pipeline
// nodes during execution. It has two namespaces: user-visible values and
// internal engine bookkeeping (retry counters, loop state).
type PipelineContext struct {
	mu       sync.RWMutex
	values   map[string]string
	internal map[string]string
}

// NewPipelineContext creates an empty pipeline context.
func NewPipelineContext() *PipelineContext {
	return &PipelineContext{
		values:   make(map[string]string),
		internal: make(map[string]string),
	}
}

// Get retrieves a value from the user-visible context.
func (c *PipelineContext) Get(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.values[key]
	return v, ok
}

// Set stores a value in the user-visible context.
func (c *PipelineContext) Set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values[key] = value
}

// Merge applies all key-value pairs from updates into the user-visible context.
func (c *PipelineContext) Merge(updates map[string]string) {
	if updates == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range updates {
		c.values[k] = v
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

// NewPipelineContextFrom creates a PipelineContext pre-populated with the
// given values. Used by the parallel handler to give each branch an isolated
// snapshot of the shared context.
func NewPipelineContextFrom(values map[string]string) *PipelineContext {
	ctx := NewPipelineContext()
	for k, v := range values {
		ctx.Set(k, v)
	}
	return ctx
}
