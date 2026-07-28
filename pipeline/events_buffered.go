// ABOUTME: Bounded, asynchronous wrappers for PipelineEventHandler and agent.EventHandler.
// ABOUTME: Decouples a slow subscriber (network I/O) from the engine goroutine with an explicit overflow policy.
package pipeline

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"github.com/2389-research/tracker/agent"
)

// OverflowPolicy decides what a buffered handler does with an event that
// arrives while its queue is full. There is deliberately no usable zero value:
// an unset policy is rejected by the constructors so a caller can never lose
// events by omission.
type OverflowPolicy string

const (
	// OverflowBlock applies backpressure: the producer (engine goroutine)
	// blocks until the forwarding goroutine frees a slot. Nothing is ever
	// dropped, but a slow subscriber slows the run down.
	OverflowBlock OverflowPolicy = "block"

	// OverflowDropOldest discards the oldest queued event to make room for
	// the new one, keeping the freshest view of the run. Preferred for
	// progress UIs.
	OverflowDropOldest OverflowPolicy = "drop_oldest"

	// OverflowDropNewest discards the arriving event, keeping the earliest
	// history. Preferred for a consumer that needs a prefix of the stream.
	OverflowDropNewest OverflowPolicy = "drop_newest"
)

// eventQueue is the shared bounded/async machinery behind the exported
// buffered handlers.
//
// Terminal-event invariant: an event that isTerminal reports true for is NEVER
// dropped, whatever the policy — it is the authoritative run-finished signal
// documented in docs/architecture/transport-boundary.md, and dropping it would
// strand a subscriber forever. To honour that without unbounded memory, a
// terminal event arriving at a full queue evicts the oldest *non-terminal*
// event; if the queue holds nothing but undelivered terminal events, the send
// blocks until the forwarding goroutine drains one (that requires a wedged
// subscriber and a queue whose whole capacity is terminal events, which a
// single run's stream does not produce).
type eventQueue[T any] struct {
	mu     sync.Mutex // guards closed and every channel operation
	closed bool
	ch     chan T

	policy     OverflowPolicy
	isTerminal func(T) bool
	deliver    func(T)

	dropped   atomic.Uint64
	wg        sync.WaitGroup
	panicOnce sync.Once
	name      string
}

func newEventQueue[T any](name string, capacity int, policy OverflowPolicy, isTerminal func(T) bool, deliver func(T)) (*eventQueue[T], error) {
	if capacity < 1 {
		return nil, fmt.Errorf("%s: capacity must be >= 1, got %d", name, capacity)
	}
	switch policy {
	case OverflowBlock, OverflowDropOldest, OverflowDropNewest:
	default:
		return nil, fmt.Errorf("%s: overflow policy must be one of %q, %q, %q, got %q",
			name, OverflowBlock, OverflowDropOldest, OverflowDropNewest, policy)
	}
	q := &eventQueue[T]{
		ch:         make(chan T, capacity),
		policy:     policy,
		isTerminal: isTerminal,
		deliver:    deliver,
		name:       name,
	}
	q.wg.Add(1)
	go q.run()
	return q, nil
}

// run drains the queue on a dedicated goroutine until close.
func (q *eventQueue[T]) run() {
	defer q.wg.Done()
	for evt := range q.ch {
		q.dispatch(evt)
	}
}

// dispatch delivers one event downstream, containing any panic so a
// misbehaving subscriber cannot kill the forwarding goroutine.
func (q *eventQueue[T]) dispatch(evt T) {
	defer q.recoverPanic()
	q.deliver(evt)
}

func (q *eventQueue[T]) recoverPanic() {
	if r := recover(); r != nil {
		q.panicOnce.Do(func() {
			fmt.Fprintf(os.Stderr, "tracker: %s recovered from downstream handler panic: %v (further panics suppressed)\n", q.name, r)
		})
	}
}

// push hands evt to the forwarding goroutine. It is safe to call from any
// goroutine, including after close.
func (q *eventQueue[T]) push(evt T) {
	if q.enqueue(evt) {
		// Post-close terminal event: deliver synchronously rather than drop
		// the run-finished signal. Wait for the forwarding goroutine first so
		// the terminal event still lands after everything Close flushed, and
		// never concurrently with it.
		q.wg.Wait()
		q.dispatch(evt)
	}
}

// enqueue queues evt, applying the overflow policy. It reports whether the
// caller must deliver evt inline instead (post-close terminal event).
func (q *eventQueue[T]) enqueue(evt T) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		if q.isTerminal(evt) {
			return true
		}
		q.dropped.Add(1)
		return false
	}
	select {
	case q.ch <- evt:
	default:
		q.handleFull(evt)
	}
	return false
}

// handleFull resolves an event that did not fit in the queue. Caller holds mu,
// so no other producer can consume the slot freed by an eviction.
func (q *eventQueue[T]) handleFull(evt T) {
	switch {
	case q.policy == OverflowBlock:
		// Backpressure drops nothing, terminal events included.
		q.ch <- evt
	case q.isTerminal(evt):
		// Never dropped. Blocks only if the queue is all terminal events.
		q.evictOldest()
		q.ch <- evt
	case q.policy == OverflowDropNewest:
		q.dropped.Add(1)
	default: // OverflowDropOldest
		if q.evictOldest() {
			q.ch <- evt
		} else {
			q.dropped.Add(1)
		}
	}
}

// evictOldest frees one slot by discarding the oldest queued event. It reports
// false when the head is a terminal event: that event is re-queued (behind the
// remaining events) rather than discarded, and the queue stays full.
func (q *eventQueue[T]) evictOldest() bool {
	select {
	case old := <-q.ch:
		if q.isTerminal(old) {
			q.ch <- old // a slot is free and mu excludes other producers
			return false
		}
		q.dropped.Add(1)
		return true
	default:
		return true // drained by the forwarding goroutine; room available
	}
}

// close stops accepting events, flushes what is queued, and waits for the
// forwarding goroutine to exit. Idempotent and safe to call concurrently.
func (q *eventQueue[T]) close() error {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		close(q.ch)
	}
	q.mu.Unlock()
	q.wg.Wait()
	return nil
}

// BufferedPipelineHandler wraps a PipelineEventHandler in a bounded queue
// drained by a background goroutine, so a subscriber doing network I/O (a
// control plane POSTing events) cannot block the engine goroutine.
//
// Events that do not fit are resolved by the OverflowPolicy chosen at
// construction; Dropped reports how many were discarded. An event carrying a
// non-empty TerminalStatus is never dropped under any policy — see the
// eventQueue docs for how that is guaranteed.
//
// The wrapped handler is invoked from a single goroutine while the wrapper is
// open, with one exception: a terminal event submitted after Close is
// delivered synchronously on the caller's goroutine. The wrapped handler must
// not call back into the wrapper (self-feeding an event stream deadlocks the
// same way a synchronous handler would recurse).
type BufferedPipelineHandler struct {
	q *eventQueue[PipelineEvent]
}

// NewBufferedPipelineHandler returns a buffered wrapper around inner. capacity
// must be >= 1 and policy must be one of OverflowBlock, OverflowDropOldest, or
// OverflowDropNewest — there is no implicit default, because every default
// either drops data or applies backpressure and the caller must own that
// choice. The caller must Close the returned handler to stop its goroutine.
func NewBufferedPipelineHandler(inner PipelineEventHandler, capacity int, policy OverflowPolicy) (*BufferedPipelineHandler, error) {
	if inner == nil {
		return nil, fmt.Errorf("buffered pipeline handler: inner handler must not be nil")
	}
	q, err := newEventQueue("buffered pipeline handler", capacity, policy,
		func(evt PipelineEvent) bool { return evt.TerminalStatus != "" },
		inner.HandlePipelineEvent)
	if err != nil {
		return nil, err
	}
	return &BufferedPipelineHandler{q: q}, nil
}

// HandlePipelineEvent queues evt for asynchronous delivery.
func (h *BufferedPipelineHandler) HandlePipelineEvent(evt PipelineEvent) { h.q.push(evt) }

// Dropped returns the number of events discarded so far: overflow drops plus
// non-terminal events submitted after Close. Every submitted event is either
// delivered or counted here — nothing is lost silently.
func (h *BufferedPipelineHandler) Dropped() uint64 { return h.q.dropped.Load() }

// Close flushes queued events to the wrapped handler and stops the forwarding
// goroutine. It is idempotent and always returns nil (the error return exists
// so callers can defer it uniformly with io.Closer-shaped sinks). Close waits
// for the flush, so a wrapped handler that never returns keeps Close (and, on
// OverflowBlock, the producer) waiting — bound your subscriber's I/O.
func (h *BufferedPipelineHandler) Close() error { return h.q.close() }

// BufferedAgentHandler is the agent.EventHandler equivalent of
// BufferedPipelineHandler. agent.Event carries no terminal status, so every
// event is subject to the overflow policy.
type BufferedAgentHandler struct {
	q *eventQueue[agent.Event]
}

// NewBufferedAgentHandler returns a buffered wrapper around inner. See
// NewBufferedPipelineHandler for the capacity and policy contract.
func NewBufferedAgentHandler(inner agent.EventHandler, capacity int, policy OverflowPolicy) (*BufferedAgentHandler, error) {
	if inner == nil {
		return nil, fmt.Errorf("buffered agent handler: inner handler must not be nil")
	}
	q, err := newEventQueue("buffered agent handler", capacity, policy,
		func(agent.Event) bool { return false },
		inner.HandleEvent)
	if err != nil {
		return nil, err
	}
	return &BufferedAgentHandler{q: q}, nil
}

// HandleEvent queues evt for asynchronous delivery.
func (h *BufferedAgentHandler) HandleEvent(evt agent.Event) { h.q.push(evt) }

// Dropped returns the number of events discarded so far (overflow drops plus
// events submitted after Close).
func (h *BufferedAgentHandler) Dropped() uint64 { return h.q.dropped.Load() }

// Close flushes queued events and stops the forwarding goroutine. Idempotent.
func (h *BufferedAgentHandler) Close() error { return h.q.close() }
