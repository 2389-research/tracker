// ABOUTME: Regression tests for the two review findings on the buffered event-handler seam.
// ABOUTME: P1 — backpressure only when no evictable non-terminal exists; P2 — post-Close terminal dispatch is serialized and awaited by Close.
package pipeline

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// parkingSink blocks inside the handler until release is closed, signalling
// entered on the first call so a test can drive the queue deterministically:
// once the forwarding goroutine is parked, everything else the producer pushes
// stays in the queue.
type parkingSink struct {
	entered   chan struct{}
	release   chan struct{}
	enterOnce sync.Once

	mu       sync.Mutex
	received []PipelineEvent
}

func newParkingSink() *parkingSink {
	return &parkingSink{entered: make(chan struct{}), release: make(chan struct{})}
}

func (s *parkingSink) HandlePipelineEvent(evt PipelineEvent) {
	s.enterOnce.Do(func() { close(s.entered) })
	<-s.release
	s.mu.Lock()
	defer s.mu.Unlock()
	s.received = append(s.received, evt)
}

func (s *parkingSink) terminals() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int
	for _, evt := range s.received {
		if evt.TerminalStatus != "" {
			n++
		}
	}
	return n
}

// P1: a full queue whose HEAD is a protected terminal event but which still
// holds evictable non-terminal events behind it must not force backpressure
// under a drop policy. The queue has to search past protected terminals.
func TestBufferedPipelineHandlerEvictsPastQueuedTerminal(t *testing.T) {
	for _, policy := range []OverflowPolicy{OverflowDropOldest, OverflowDropNewest} {
		t.Run(string(policy), func(t *testing.T) {
			sink := newParkingSink()
			h := mustBuffered(t, sink, 3, policy)
			defer func() {
				close(sink.release)
				if err := h.Close(); err != nil {
					t.Errorf("Close: %v", err)
				}
			}()

			// Park the forwarding goroutine on a filler event so the queue
			// contents below are deterministic.
			h.HandlePipelineEvent(PipelineEvent{Type: EventStageStarted, Message: "filler"})
			<-sink.entered

			// Queue (head first): [terminal, non-terminal, non-terminal] — full.
			h.HandlePipelineEvent(PipelineEvent{Type: EventBudgetExceeded, NodeID: "sub/child", TerminalStatus: "budget_exceeded"})
			h.HandlePipelineEvent(PipelineEvent{Type: EventStageStarted, Message: "n1"})
			h.HandlePipelineEvent(PipelineEvent{Type: EventStageCompleted, Message: "n2"})

			// The run-level terminal event arrives. Two evictable non-terminals
			// are queued, so this must not block on the wedged sink.
			done := make(chan struct{})
			go func() {
				defer close(done)
				h.HandlePipelineEvent(PipelineEvent{Type: EventPipelineFailed, TerminalStatus: "fail"})
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("producer stalled behind a wedged sink even though an evictable non-terminal event was queued")
			}
			if got := h.Dropped(); got != 1 {
				t.Errorf("Dropped() = %d, want 1 (exactly one non-terminal evicted)", got)
			}
		})
	}
}

// The eviction search must still conclude backpressure when every queued event
// is protected: nothing is dropped, and the terminal event lands once the sink
// drains.
func TestBufferedPipelineHandlerAllTerminalQueueAppliesBackpressure(t *testing.T) {
	sink := newParkingSink()
	h := mustBuffered(t, sink, 1, OverflowDropNewest)

	h.HandlePipelineEvent(PipelineEvent{Type: EventStageStarted, Message: "filler"})
	<-sink.entered
	// Queue (capacity 1) holds one protected terminal event.
	h.HandlePipelineEvent(PipelineEvent{Type: EventBudgetExceeded, NodeID: "sub/child", TerminalStatus: "budget_exceeded"})

	go func() {
		time.Sleep(50 * time.Millisecond)
		close(sink.release)
	}()
	h.HandlePipelineEvent(PipelineEvent{Type: EventPipelineFailed, TerminalStatus: "fail"})
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := h.Dropped(); got != 0 {
		t.Errorf("Dropped() = %d, want 0 — terminal events must never be evicted", got)
	}
	if got := sink.terminals(); got != 2 {
		t.Errorf("terminal events delivered = %d, want 2", got)
	}
}

// overlapSink reports whether it was ever entered by two goroutines at once.
type overlapSink struct {
	active   atomic.Int32
	overlaps atomic.Int32
	calls    atomic.Int32
}

func (s *overlapSink) HandlePipelineEvent(PipelineEvent) {
	if s.active.Add(1) > 1 {
		s.overlaps.Add(1)
	}
	time.Sleep(20 * time.Millisecond)
	s.calls.Add(1)
	s.active.Add(-1)
}

// P2a: terminal events submitted concurrently after Close must not invoke the
// wrapped handler concurrently — the wrapper promises serialized delivery.
func TestBufferedPipelineHandlerPostCloseTerminalDispatchIsSerialized(t *testing.T) {
	sink := &overlapSink{}
	h := mustBuffered(t, sink, 4, OverflowDropNewest)
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	const n = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			h.HandlePipelineEvent(PipelineEvent{Type: EventPipelineCompleted, TerminalStatus: "success"})
		}()
	}
	close(start)
	wg.Wait()

	if got := sink.calls.Load(); got != n {
		t.Errorf("delivered %d terminal events, want %d", got, n)
	}
	if got := sink.overlaps.Load(); got != 0 {
		t.Errorf("wrapped handler entered concurrently %d times; post-Close terminal dispatch must be serialized", got)
	}
}

// P2b: Close must not return while a post-Close terminal dispatch registered
// before it is still writing to the sink — otherwise the caller can tear down a
// non-thread-safe sink mid-write.
func TestBufferedPipelineHandlerCloseWaitsForPostCloseDispatch(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var completed atomic.Bool
	inner := PipelineEventHandlerFunc(func(PipelineEvent) {
		close(entered)
		<-release
		completed.Store(true)
	})
	h := mustBuffered(t, inner, 4, OverflowDropNewest)
	if err := h.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	go h.HandlePipelineEvent(PipelineEvent{Type: EventPipelineCompleted, TerminalStatus: "success"})
	<-entered // the post-Close dispatch is now inside the sink

	closeReturned := make(chan struct{})
	go func() {
		defer close(closeReturned)
		if err := h.Close(); err != nil {
			t.Errorf("second Close: %v", err)
		}
	}()
	select {
	case <-closeReturned:
		t.Fatal("Close returned while a post-Close terminal dispatch was still writing to the sink")
	case <-time.After(200 * time.Millisecond):
	}

	close(release)
	select {
	case <-closeReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after the in-flight dispatch completed")
	}
	if !completed.Load() {
		t.Error("dispatch did not complete")
	}
}
