// ABOUTME: Central state container for the TUI with Apply(msg) pattern.
// ABOUTME: Holds node entries, statuses, thinking state, and pipeline completion state.
package tui

import (
	"time"

	"github.com/2389-research/tracker/llm"
)

// NodeState represents the execution state of a pipeline node.
type NodeState int

const (
	NodePending NodeState = iota
	NodeRunning
	NodeDone
	NodeFailed
	NodeRetrying
)

// NodePhase represents the current activity phase of a running node.
type NodePhase int

const (
	PhaseIdle NodePhase = iota
	PhasePreparing
	PhaseWaiting
	PhaseThinking
	PhaseTooling
	PhaseCompacting
	PhaseRouting
)

// NodeEntry identifies a node in the pipeline with its display label.
type NodeEntry struct {
	ID    string
	Label string
}

// nodeInfo holds per-node mutable state.
type nodeInfo struct {
	status       NodeState
	errMsg       string
	thinking     bool
	retryMsg     string
	waiting      bool      // true when waiting for provider to respond (before thinking starts)
	phase        NodePhase // current activity phase
	phaseStarted time.Time // when current phase started (for elapsed time tracking)
}

// StateStore is the central state container for the TUI.
type StateStore struct {
	nodes        []NodeEntry
	nodeState    map[string]*nodeInfo
	pipelineDone bool
	pipelineErr  string
	Tokens       *llm.TokenTracker
}

// NewStateStore creates a StateStore with an optional TokenTracker.
func NewStateStore(tokens *llm.TokenTracker) *StateStore {
	return &StateStore{
		nodeState: make(map[string]*nodeInfo),
		Tokens:    tokens,
	}
}

// SetNodes sets the ordered list of pipeline nodes.
func (s *StateStore) SetNodes(entries []NodeEntry) {
	s.nodes = entries
	for _, e := range entries {
		if _, ok := s.nodeState[e.ID]; !ok {
			s.nodeState[e.ID] = &nodeInfo{}
		}
	}
}

// Nodes returns the ordered node list.
func (s *StateStore) Nodes() []NodeEntry { return s.nodes }

// NodeStatus returns the current state of a node.
func (s *StateStore) NodeStatus(id string) NodeState {
	if ni, ok := s.nodeState[id]; ok {
		return ni.status
	}
	return NodePending
}

// NodeError returns the error message for a failed node.
func (s *StateStore) NodeError(id string) string {
	if ni, ok := s.nodeState[id]; ok {
		return ni.errMsg
	}
	return ""
}

// NodeRetryMessage returns the retry message for a retrying node.
func (s *StateStore) NodeRetryMessage(id string) string {
	if ni, ok := s.nodeState[id]; ok {
		return ni.retryMsg
	}
	return ""
}

// IsThinking returns whether a node is in the thinking state.
func (s *StateStore) IsThinking(id string) bool {
	if ni, ok := s.nodeState[id]; ok {
		return ni.thinking
	}
	return false
}

// IsWaiting returns whether the node is waiting for provider response.
func (s *StateStore) IsWaiting(id string) bool {
	if ni, ok := s.nodeState[id]; ok {
		return ni.waiting
	}
	return false
}

// GetPhase returns the current activity phase of a node.
func (s *StateStore) GetPhase(id string) NodePhase {
	if ni, ok := s.nodeState[id]; ok {
		return ni.phase
	}
	return PhaseIdle
}

// PhaseElapsed returns how long the node has been in its current phase.
func (s *StateStore) PhaseElapsed(id string) time.Duration {
	if ni, ok := s.nodeState[id]; ok && ni.phase != PhaseIdle && !ni.phaseStarted.IsZero() {
		return time.Since(ni.phaseStarted)
	}
	return 0
}

// PipelineDone returns whether the pipeline has completed (success or failure).
func (s *StateStore) PipelineDone() bool { return s.pipelineDone }

// PipelineError returns the pipeline error message, if any.
func (s *StateStore) PipelineError() string { return s.pipelineErr }

// Progress returns the count of completed nodes and total nodes.
func (s *StateStore) Progress() (done, total int) {
	total = len(s.nodes)
	for _, e := range s.nodes {
		if ni, ok := s.nodeState[e.ID]; ok && ni.status == NodeDone {
			done++
		}
	}
	return
}

// ensure lazily creates node info for unknown node IDs.
func (s *StateStore) ensure(id string) *nodeInfo {
	ni, ok := s.nodeState[id]
	if !ok {
		ni = &nodeInfo{}
		s.nodeState[id] = ni
	}
	return ni
}

// Apply updates state based on a typed message.
func (s *StateStore) Apply(msg interface{}) {
	switch m := msg.(type) {
	case MsgNodeStarted:
		s.ensure(m.NodeID).status = NodeRunning
	case MsgNodeCompleted:
		s.ensure(m.NodeID).status = NodeDone
	case MsgNodeFailed:
		ni := s.ensure(m.NodeID)
		ni.status = NodeFailed
		ni.errMsg = m.Error
	case MsgNodeRetrying:
		ni := s.ensure(m.NodeID)
		ni.status = NodeRetrying
		ni.retryMsg = m.Message
	case MsgPipelineCompleted:
		s.pipelineDone = true
	case MsgPipelineFailed:
		s.pipelineDone = true
		s.pipelineErr = m.Error
	case MsgThinkingStarted:
		s.ensure(m.NodeID).thinking = true
		s.ensure(m.NodeID).waiting = false // clear waiting state when thinking starts
	case MsgThinkingStopped:
		s.ensure(m.NodeID).thinking = false
	case MsgLLMRequestPreparing:
		s.ensure(m.NodeID).waiting = true // set waiting state before provider responds
	}
}
