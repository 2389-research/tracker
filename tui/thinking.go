// ABOUTME: Per-node LLM thinking state tracker with animation frames and elapsed time.
// ABOUTME: Manages a global tick counter that advances all active nodes through spinner frames.
package tui

import "time"

// thinkingState holds per-node thinking metadata.
type thinkingState struct {
	startedAt time.Time
	frame     int
	active    bool
	toolName  string // non-empty when a tool is executing (distinct from LLM thinking)
}

// ThinkingTracker manages per-node thinking and tool execution animation state.
type ThinkingTracker struct {
	nodes map[string]*thinkingState
	tick  int
}

// NewThinkingTracker creates a ThinkingTracker with no active nodes.
func NewThinkingTracker() *ThinkingTracker {
	return &ThinkingTracker{
		nodes: make(map[string]*thinkingState),
	}
}

// Start begins thinking for a node, recording the current time.
func (tr *ThinkingTracker) Start(nodeID string) {
	tr.StartAt(nodeID, time.Now())
}

// StartAt begins thinking for a node at a specific time.
func (tr *ThinkingTracker) StartAt(nodeID string, t time.Time) {
	tr.nodes[nodeID] = &thinkingState{
		startedAt: t,
		frame:     tr.tick,
		active:    true,
	}
}

// Stop ends thinking for a node.
func (tr *ThinkingTracker) Stop(nodeID string) {
	if ns, ok := tr.nodes[nodeID]; ok {
		ns.active = false
	}
}

// IsThinking returns whether a node is currently in the thinking state.
func (tr *ThinkingTracker) IsThinking(nodeID string) bool {
	if ns, ok := tr.nodes[nodeID]; ok {
		return ns.active
	}
	return false
}

// Frame returns the current animation frame character for a thinking node.
// Returns empty string if the node is not thinking.
func (tr *ThinkingTracker) Frame(nodeID string) string {
	ns, ok := tr.nodes[nodeID]
	if !ok || !ns.active {
		return ""
	}
	idx := (tr.tick - ns.frame) % len(ThinkingFrames)
	return ThinkingFrames[idx]
}

// Elapsed returns how long a node has been thinking.
func (tr *ThinkingTracker) Elapsed(nodeID string) time.Duration {
	if ns, ok := tr.nodes[nodeID]; ok {
		return time.Since(ns.startedAt)
	}
	return 0
}

// StartTool marks a node as executing a tool (distinct from LLM thinking).
// Resets the phase timestamp so the elapsed display reflects tool duration
// rather than accumulated thinking+tool time.
func (tr *ThinkingTracker) StartTool(nodeID, toolName string) {
	ns, ok := tr.nodes[nodeID]
	if !ok {
		ns = &thinkingState{startedAt: time.Now(), frame: tr.tick}
		tr.nodes[nodeID] = ns
	}
	ns.toolName = toolName
	ns.startedAt = time.Now()
}

// StopTool clears the tool-running state for a node.
func (tr *ThinkingTracker) StopTool(nodeID string) {
	if ns, ok := tr.nodes[nodeID]; ok {
		ns.toolName = ""
	}
}

// IsToolRunning returns whether a node is currently executing a tool.
func (tr *ThinkingTracker) IsToolRunning(nodeID string) bool {
	if ns, ok := tr.nodes[nodeID]; ok {
		return ns.toolName != ""
	}
	return false
}

// ToolName returns the name of the tool currently running on a node.
func (tr *ThinkingTracker) ToolName(nodeID string) string {
	if ns, ok := tr.nodes[nodeID]; ok {
		return ns.toolName
	}
	return ""
}

// Tick advances the global animation counter by one frame.
func (tr *ThinkingTracker) Tick() {
	tr.tick++
}
