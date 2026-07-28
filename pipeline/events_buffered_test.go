// ABOUTME: Tests for the bounded/async event-handler seam (BufferedPipelineHandler, BufferedAgentHandler).
// ABOUTME: Pins non-stalling producers, per-policy drop accounting, the terminal-event invariant, Close, and panic containment.
package pipeline

import (
	"sync"
	"testing"
	"time"

	"github.com/2389-research/tracker/agent"
)

// collector is a thread-safe recording handler.
type collector struct {
	mu     sync.Mutex
	events []PipelineEvent
	gate   chan struct{} // if non-nil, every call blocks until it is closed
}

func (c *collector) HandlePipelineEvent(evt PipelineEvent) {
	if c.gate != nil {
		<-c.gate
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, evt)
}

func (c *collector) snapshot() []PipelineEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]PipelineEvent, len(c.events))
	copy(out, c.events)
	return out
}

func (c *collector) count() int { return len(c.snapshot()) }

func mustBuffered(t *testing.T, inner PipelineEventHandler, capacity int, policy OverflowPolicy) *BufferedPipelineHandler {
	t.Helper()
	h, err := NewBufferedPipelineHandler(inner, capacity, policy)
	if err != nil {
		t.Fatalf("NewBufferedPipelineHandler: %v", err)
	}
	return h
}

func TestBufferedPipelineHandlerConstructorValidation(t *testing.T) {
	if _, err := NewBufferedPipelineHandler(nil, 4, OverflowBlock); err == nil {
		t.Error("expected error for nil inner handler")
	}
	if _, err := NewBufferedPipelineHandler(&collector{}, 0, OverflowBlock); err == nil {
		t.Error("expected error for non-positive capacity")
	}
	if _, err := NewBufferedPipelineHandler(&collector{}, 4, OverflowPolicy("")); err == nil {
		t.Error("expected error for empty (unset) policy — the choice must be explicit")
	}
	if _, err := NewBufferedPipelineHandler(&collector{}, 4, OverflowPolicy("whatever")); err == nil {
		t.Error("expected error for unknown policy")
	}
}

func TestBufferedPipelineHandlerSlowHandlerDoesNotStallProducer(t *testing.T) {
	gate := make(chan struct{})
	inner := &collector{gate: gate}
	h := mustBuffered(t, inner, 2, OverflowDropOldest)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			h.HandlePipelineEvent(PipelineEvent{Type: EventStageStarted, NodeID: "n"})
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("producer stalled behind a blocked downstream handler")
	}
	if h.Dropped() == 0 {
		t.Error("expected dropped events while the downstream handler was blocked")
	}
	close(gate)
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestBufferedPipelineHandlerDropNewestCountsDrops(t *testing.T) {
	gate := make(chan struct{})
	inner := &collector{gate: gate}
	h := mustBuffered(t, inner, 1, OverflowDropNewest)

	for i := 0; i < 50; i++ {
		h.HandlePipelineEvent(PipelineEvent{Type: EventStageStarted})
	}
	if got := h.Dropped(); got == 0 {
		t.Fatalf("Dropped() = 0, want > 0")
	}
	close(gate)
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Every event is either delivered or counted as dropped — no silent loss.
	if total := uint64(inner.count()) + h.Dropped(); total != 50 {
		t.Errorf("delivered+dropped = %d, want 50", total)
	}
}

func TestBufferedPipelineHandlerDropOldestKeepsNewest(t *testing.T) {
	gate := make(chan struct{})
	inner := &collector{gate: gate}
	h := mustBuffered(t, inner, 2, OverflowDropOldest)

	for i := 0; i < 20; i++ {
		h.HandlePipelineEvent(PipelineEvent{Type: EventStageStarted, Message: string(rune('a' + i))})
	}
	if h.Dropped() == 0 {
		t.Fatal("expected drops under drop-oldest overflow")
	}
	close(gate)
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got := inner.snapshot()
	if len(got) == 0 {
		t.Fatal("no events delivered")
	}
	if last := got[len(got)-1].Message; last != string(rune('a'+19)) {
		t.Errorf("last delivered message = %q, want the newest event %q", last, string(rune('a'+19)))
	}
	if total := uint64(len(got)) + h.Dropped(); total != 20 {
		t.Errorf("delivered+dropped = %d, want 20", total)
	}
}

func TestBufferedPipelineHandlerBlockPolicyDropsNothing(t *testing.T) {
	inner := &collector{}
	h := mustBuffered(t, inner, 1, OverflowBlock)
	for i := 0; i < 100; i++ {
		h.HandlePipelineEvent(PipelineEvent{Type: EventStageStarted})
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if h.Dropped() != 0 {
		t.Errorf("Dropped() = %d, want 0 under OverflowBlock", h.Dropped())
	}
	if inner.count() != 100 {
		t.Errorf("delivered %d events, want 100", inner.count())
	}
}

// A terminal event arriving at a *full* queue under OverflowBlock must apply
// backpressure, not evict a non-terminal event to make room.
func TestBufferedPipelineHandlerBlockPolicyDropsNothingWhenFull(t *testing.T) {
	gate := make(chan struct{})
	inner := &collector{gate: gate}
	h := mustBuffered(t, inner, 1, OverflowBlock)
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(gate)
	}()
	for i := 0; i < 20; i++ {
		h.HandlePipelineEvent(PipelineEvent{Type: EventStageStarted})
	}
	h.HandlePipelineEvent(PipelineEvent{Type: EventPipelineCompleted, TerminalStatus: "success"})
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if h.Dropped() != 0 {
		t.Errorf("Dropped() = %d, want 0 under OverflowBlock", h.Dropped())
	}
	if inner.count() != 21 {
		t.Errorf("delivered %d events, want 21", inner.count())
	}
}

// The run-finished signal must survive every overflow policy.
func TestBufferedPipelineHandlerTerminalEventNeverDropped(t *testing.T) {
	for _, policy := range []OverflowPolicy{OverflowBlock, OverflowDropOldest, OverflowDropNewest} {
		t.Run(string(policy), func(t *testing.T) {
			gate := make(chan struct{})
			inner := &collector{gate: gate}
			h := mustBuffered(t, inner, 1, policy)

			flood := func() {
				for i := 0; i < 100; i++ {
					h.HandlePipelineEvent(PipelineEvent{Type: EventStageStarted})
				}
			}
			if policy == OverflowBlock {
				// Block policy would deadlock against the gate; unblock first.
				close(gate)
				flood()
			} else {
				flood()
			}
			h.HandlePipelineEvent(PipelineEvent{
				Type:           EventPipelineCompleted,
				TerminalStatus: "success",
			})
			if policy != OverflowBlock {
				close(gate)
			}
			if err := h.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			var terminals int
			for _, evt := range inner.snapshot() {
				if evt.TerminalStatus != "" {
					terminals++
				}
			}
			if terminals != 1 {
				t.Fatalf("terminal events delivered = %d, want 1 (policy %s)", terminals, policy)
			}
		})
	}
}

// Multiple terminal events (a scoped subgraph budget_exceeded plus the run
// terminal) must all survive a saturated queue.
func TestBufferedPipelineHandlerMultipleTerminalEventsSurvive(t *testing.T) {
	gate := make(chan struct{})
	inner := &collector{gate: gate}
	h := mustBuffered(t, inner, 1, OverflowDropNewest)

	for i := 0; i < 10; i++ {
		h.HandlePipelineEvent(PipelineEvent{Type: EventStageStarted})
	}
	// Release the downstream handler shortly: a second terminal event arriving
	// at a queue whose head is already an undelivered terminal event applies
	// backpressure rather than dropping either one.
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(gate)
	}()
	h.HandlePipelineEvent(PipelineEvent{Type: EventBudgetExceeded, NodeID: "sub/child", TerminalStatus: "budget_exceeded"})
	h.HandlePipelineEvent(PipelineEvent{Type: EventPipelineFailed, TerminalStatus: "fail"})
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	var terminals int
	for _, evt := range inner.snapshot() {
		if evt.TerminalStatus != "" {
			terminals++
		}
	}
	if terminals != 2 {
		t.Fatalf("terminal events delivered = %d, want 2", terminals)
	}
}

// A terminal event submitted after Close is still delivered, not dropped.
func TestBufferedPipelineHandlerTerminalAfterCloseDelivered(t *testing.T) {
	inner := &collector{}
	h := mustBuffered(t, inner, 4, OverflowDropNewest)
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	h.HandlePipelineEvent(PipelineEvent{Type: EventStageStarted})
	h.HandlePipelineEvent(PipelineEvent{Type: EventPipelineCompleted, TerminalStatus: "success"})

	got := inner.snapshot()
	if len(got) != 1 || got[0].TerminalStatus != "success" {
		t.Fatalf("after Close: delivered %+v, want only the terminal event", got)
	}
	if h.Dropped() != 1 {
		t.Errorf("Dropped() = %d, want 1 (the non-terminal post-Close event)", h.Dropped())
	}
}

func TestBufferedPipelineHandlerCloseFlushesAndIsIdempotent(t *testing.T) {
	inner := &collector{}
	h := mustBuffered(t, inner, 64, OverflowBlock)
	for i := 0; i < 64; i++ {
		h.HandlePipelineEvent(PipelineEvent{Type: EventStageStarted})
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if inner.count() != 64 {
		t.Errorf("Close did not flush: delivered %d, want 64", inner.count())
	}
	for i := 0; i < 3; i++ {
		if err := h.Close(); err != nil {
			t.Fatalf("Close #%d: %v", i+2, err)
		}
	}
}

func TestBufferedPipelineHandlerConcurrentCloseAndSend(t *testing.T) {
	h := mustBuffered(t, &collector{}, 8, OverflowDropOldest)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				h.HandlePipelineEvent(PipelineEvent{Type: EventStageStarted})
			}
		}()
	}
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := h.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock between Close and concurrent sends")
	}
}

func TestBufferedPipelineHandlerContainsPanickingHandler(t *testing.T) {
	var delivered int
	var mu sync.Mutex
	inner := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		mu.Lock()
		delivered++
		mu.Unlock()
		panic("downstream boom")
	})
	h := mustBuffered(t, inner, 4, OverflowBlock)
	for i := 0; i < 5; i++ {
		h.HandlePipelineEvent(PipelineEvent{Type: EventStageStarted})
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if delivered != 5 {
		t.Errorf("delivered %d events, want 5 — the forwarding goroutine died on the first panic", delivered)
	}
}

type agentCollector struct {
	mu     sync.Mutex
	events []agent.Event
	gate   chan struct{}
}

func (c *agentCollector) HandleEvent(evt agent.Event) {
	if c.gate != nil {
		<-c.gate
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, evt)
}

func (c *agentCollector) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.events)
}

func TestBufferedAgentHandler(t *testing.T) {
	if _, err := NewBufferedAgentHandler(nil, 4, OverflowBlock); err == nil {
		t.Error("expected error for nil inner handler")
	}
	if _, err := NewBufferedAgentHandler(&agentCollector{}, 4, OverflowPolicy("nope")); err == nil {
		t.Error("expected error for unknown policy")
	}

	inner := &agentCollector{}
	h, err := NewBufferedAgentHandler(inner, 4, OverflowBlock)
	if err != nil {
		t.Fatalf("NewBufferedAgentHandler: %v", err)
	}
	for i := 0; i < 20; i++ {
		h.HandleEvent(agent.Event{Type: agent.EventSessionStart})
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if inner.count() != 20 {
		t.Errorf("delivered %d events, want 20", inner.count())
	}
	if h.Dropped() != 0 {
		t.Errorf("Dropped() = %d, want 0", h.Dropped())
	}
}

func TestBufferedAgentHandlerSlowHandlerDoesNotStallProducer(t *testing.T) {
	gate := make(chan struct{})
	inner := &agentCollector{gate: gate}
	h, err := NewBufferedAgentHandler(inner, 2, OverflowDropNewest)
	if err != nil {
		t.Fatalf("NewBufferedAgentHandler: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			h.HandleEvent(agent.Event{Type: agent.EventTextDelta, Text: "x"})
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("producer stalled behind a blocked downstream agent handler")
	}
	if h.Dropped() == 0 {
		t.Error("expected dropped agent events under drop-newest overflow")
	}
	close(gate)
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
