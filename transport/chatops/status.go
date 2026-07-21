// ABOUTME: The live run status card — one message per run, updated in place as
// ABOUTME: the pipeline event stream flows, so a thread shows the run happening.
package chatops

import (
	"sync"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

// StatusRenderer is an optional ThreadUI capability: post-or-update a single
// status message for the run. A transport that implements it (Slack via
// chat.update; a future Discord via message edit) gets the live card; one that
// doesn't just falls back to the discrete notifier posts.
type StatusRenderer interface {
	UpsertStatus(card StatusCard) error
}

// StatusNode is one node's lamp in the card.
type StatusNode struct {
	Label string
	State string // "pending" | "active" | "done" | "failed"
}

// StatusCard is the transport-neutral snapshot the renderer draws. It is a plain
// value so a renderer can hold it without racing the tracker.
type StatusCard struct {
	Workflow    string
	State       string // "running" | "success" | "fail" | "budget_exceeded" | "validation_overridden"
	Nodes       []StatusNode
	CurrentNode string
	DoneCount   int
	TotalCount  int
	CostUSD     float64
	BudgetUSD   float64 // 0 = no ceiling
	Tokens      int
	Elapsed     time.Duration
}

// statusTracker folds a run's pipeline events into a StatusCard and pushes it to
// the renderer, throttled so a chatty stream doesn't hammer the update API. It
// implements pipeline.PipelineEventHandler.
type statusTracker struct {
	r       StatusRenderer
	now     func() time.Time
	minPush time.Duration

	mu       sync.Mutex
	card     StatusCard
	idx      map[string]int // node id → index into card.Nodes
	start    time.Time
	lastPush time.Time
}

func newStatusTracker(r StatusRenderer, workflow string, budgetUSD float64) *statusTracker {
	return &statusTracker{
		r:       r,
		now:     time.Now,
		minPush: 1200 * time.Millisecond,
		idx:     map[string]int{},
		card:    StatusCard{Workflow: workflow, State: "running", BudgetUSD: budgetUSD},
	}
}

// HandlePipelineEvent updates the card and pushes it. Terminal and start events
// push immediately; progress/cost events are throttled.
func (s *statusTracker) HandlePipelineEvent(evt pipeline.PipelineEvent) {
	s.mu.Lock()
	force := s.apply(evt)
	if s.start.IsZero() {
		s.start = s.now()
	}
	s.card.Elapsed = s.now().Sub(s.start)
	card := s.snapshot()
	push := force || s.now().Sub(s.lastPush) >= s.minPush
	if push {
		s.lastPush = s.now()
	}
	s.mu.Unlock()

	if push {
		_ = s.r.UpsertStatus(card)
	}
}

// apply mutates the card for one event; returns true if the change must push now.
func (s *statusTracker) apply(evt pipeline.PipelineEvent) (force bool) {
	force = s.applyType(evt)
	if evt.TerminalStatus != "" {
		s.card.State = evt.TerminalStatus
		s.card.CurrentNode = ""
		force = true
	}
	s.recount()
	return force
}

// applyType folds one event's type-specific change into the card; returns true
// when that change should push immediately (the run started).
func (s *statusTracker) applyType(evt pipeline.PipelineEvent) bool {
	switch evt.Type {
	case pipeline.EventPipelineStarted:
		if evt.Snapshot != nil {
			s.seedNodes(evt.Snapshot)
		}
		return true
	case pipeline.EventStageStarted:
		s.setNode(evt.NodeID, "active")
		s.card.CurrentNode = s.label(evt.NodeID)
	case pipeline.EventStageCompleted:
		s.setNode(evt.NodeID, "done")
	case pipeline.EventStageFailed:
		s.setNode(evt.NodeID, "failed")
	case pipeline.EventCostUpdated:
		if evt.Cost != nil {
			s.card.CostUSD = evt.Cost.TotalCostUSD
			s.card.Tokens = evt.Cost.TotalTokens
		}
	}
	return false
}

func (s *statusTracker) seedNodes(snap *pipeline.RunSnapshot) {
	done := make(map[string]bool, len(snap.CompletedNodes))
	for _, id := range snap.CompletedNodes {
		done[id] = true
	}
	s.card.Nodes = s.card.Nodes[:0]
	s.idx = make(map[string]int, len(snap.Nodes))
	for _, n := range snap.Nodes {
		label := n.Label
		if label == "" {
			label = n.ID
		}
		state := "pending"
		if done[n.ID] {
			state = "done"
		}
		s.idx[n.ID] = len(s.card.Nodes)
		s.card.Nodes = append(s.card.Nodes, StatusNode{Label: label, State: state})
	}
	s.card.TotalCount = len(s.card.Nodes)
}

func (s *statusTracker) setNode(id, state string) {
	if i, ok := s.idx[id]; ok {
		s.card.Nodes[i].State = state
	}
}

func (s *statusTracker) label(id string) string {
	if i, ok := s.idx[id]; ok {
		return s.card.Nodes[i].Label
	}
	return id
}

func (s *statusTracker) recount() {
	c := 0
	for _, n := range s.card.Nodes {
		if n.State == "done" {
			c++
		}
	}
	s.card.DoneCount = c
}

// snapshot returns a copy (with its own Nodes slice) so the renderer never races
// a later mutation.
func (s *statusTracker) snapshot() StatusCard {
	c := s.card
	c.Nodes = append([]StatusNode(nil), s.card.Nodes...)
	return c
}
