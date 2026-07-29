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
// Protected-event invariant: an event that isProtected reports true for is
// NEVER dropped, whatever the policy. Two classes are protected: terminal
// events (the authoritative run-finished signal) and gate lifecycle events
// (EventGateOpened/EventGateResolved), whose atomicity the transport boundary
// guarantees — see docs/architecture/transport-boundary.md. Dropping a terminal
// would strand a subscriber forever; dropping a gate event would leave an
// event-sourcing consumer with a half-open gate (opened delivered, resolved
// gone) or an orphan resolution. To honour that without unbounded memory, a
// protected event arriving at a full queue evicts the oldest *evictable*
// (unprotected) event while keeping every retained event in its original order,
// so a gate_opened is never reordered behind its gate_resolved. Only a queue
// whose every element is an undelivered protected event applies backpressure
// instead — with a wedged subscriber that is the sole alternative to dropping a
// protected signal. Gate events are low-frequency, so extending protection to
// them cannot unbound the queue.
type eventQueue[T any] struct {
	mu     sync.Mutex // guards closed, inflight, and every channel operation
	closed bool
	ch     chan T

	policy      OverflowPolicy
	isProtected func(T) bool
	deliver     func(T)

	dropped   atomic.Uint64
	wg        sync.WaitGroup
	panicOnce sync.Once
	name      string

	// postCloseMu serializes the synchronous post-close terminal dispatch path
	// so two such events never enter the wrapped handler concurrently. The
	// forwarding goroutine cannot overlap with it: that path runs only after
	// wg.Wait reports the goroutine has exited.
	postCloseMu sync.Mutex
	// inflight counts registered post-close dispatches (guarded by mu); idle is
	// broadcast when it drains so close can wait them out.
	inflight int
	idle     *sync.Cond
}

func newEventQueue[T any](name string, capacity int, policy OverflowPolicy, isProtected func(T) bool, deliver func(T)) (*eventQueue[T], error) {
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
		ch:          make(chan T, capacity),
		policy:      policy,
		isProtected: isProtected,
		deliver:     deliver,
		name:        name,
	}
	q.idle = sync.NewCond(&q.mu)
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
	if !q.enqueue(evt) {
		return
	}
	// Post-close protected event (terminal or gate): deliver synchronously
	// rather than drop it. enqueue registered it as in-flight so close waits for
	// it; wg.Wait orders it after everything close flushed, and postCloseMu
	// keeps concurrent post-close dispatches from overlapping in the sink.
	defer q.endPostClose()
	q.wg.Wait()
	q.postCloseMu.Lock()
	defer q.postCloseMu.Unlock()
	q.dispatch(evt)
}

// endPostClose retires an in-flight post-close dispatch and wakes close when the
// last one drains.
func (q *eventQueue[T]) endPostClose() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.inflight--
	if q.inflight == 0 {
		q.idle.Broadcast()
	}
}

// enqueue queues evt, applying the overflow policy. It reports whether the
// caller must deliver evt inline instead (post-close terminal event).
func (q *eventQueue[T]) enqueue(evt T) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		if q.isProtected(evt) {
			q.inflight++ // close waits for this dispatch
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
		// Backpressure drops nothing, protected events included.
		q.ch <- evt
	case q.isProtected(evt):
		// Never dropped. evictOldest frees a slot unless every queued event is
		// itself protected, in which case the send below is the only alternative
		// to dropping a protected signal.
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

// evictOldest frees one slot by discarding the oldest *evictable* (unprotected)
// event while preserving the relative order of every retained event. It drains
// the queue into a slice, drops the first unprotected element, and re-enqueues
// the remainder in their original order. Backpressure (a false result) is
// reported only when every queued event is protected. Caller holds mu, so no
// other producer can consume a freed slot or the re-enqueue path; the
// forwarding goroutine only removes events, which can only add room.
//
// Draining to a slice — rather than rotating protected events to the tail in
// place — is what keeps protected events in order. In-place rotation moves a
// protected head *behind* everything queued after it, so an unprotected event
// interleaved between a gate_opened and its gate_resolved would leave the pair
// reordered once that interleaved event is dropped. Preserving order here means
// a gate_opened is never delivered after its gate_resolved.
func (q *eventQueue[T]) evictOldest() bool {
	n := len(q.ch)
	buf := make([]T, 0, n)
	dropped := false
	for range n {
		select {
		case old := <-q.ch:
			if !dropped && !q.isProtected(old) {
				q.dropped.Add(1)
				dropped = true
				continue
			}
			buf = append(buf, old)
		default:
			// Forwarding goroutine drained faster than expected; stop early.
		}
	}
	for _, evt := range buf {
		q.ch <- evt // holding mu, and we re-enqueue no more than we removed
	}
	return len(q.ch) < cap(q.ch)
}

// close stops accepting events, flushes what is queued, waits for the
// forwarding goroutine to exit, and then waits out any post-close terminal
// dispatch already registered — so a caller can tear down a non-thread-safe sink
// as soon as close returns. Idempotent and safe to call concurrently.
func (q *eventQueue[T]) close() error {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		close(q.ch)
	}
	q.mu.Unlock()
	q.wg.Wait()
	q.mu.Lock()
	for q.inflight > 0 {
		q.idle.Wait()
	}
	q.mu.Unlock()
	return nil
}

// BufferedPipelineHandler wraps a PipelineEventHandler in a bounded queue
// drained by a background goroutine, so a subscriber doing network I/O (a
// control plane POSTing events) cannot block the engine goroutine.
//
// Events that do not fit are resolved by the OverflowPolicy chosen at
// construction; Dropped reports how many were discarded. Protected events are
// never dropped under any policy — an event carrying a non-empty TerminalStatus
// (the run-finished signal) and gate lifecycle events (EventGateOpened /
// EventGateResolved, so a consumer never sees a half-open gate). See the
// eventQueue docs for how that is guaranteed.
//
// Delivery is serialized: the wrapper never invokes the wrapped handler from
// two goroutines at once. While open, delivery is the forwarding goroutine's;
// a terminal event submitted after Close is delivered synchronously on the
// caller's goroutine, after the flush completes and serialized against other
// post-Close terminals. Close waits for any such dispatch registered before it,
// so the sink is quiet once Close returns (a caller that submits events after
// Close has already returned owns that ordering). The wrapped handler must not
// call back into the wrapper (self-feeding an event stream deadlocks the same
// way a synchronous handler would recurse).
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
		// Protected (never dropped): terminal events and gate lifecycle events
		// (EventGateOpened/EventGateResolved, which carry a non-nil Gate). The
		// gate pair must survive a lossy policy intact so a consumer never sees a
		// half-open gate — see docs/architecture/transport-boundary.md §3.
		func(evt PipelineEvent) bool { return evt.TerminalStatus != "" || evt.Gate != nil },
		inner.HandlePipelineEvent)
	if err != nil {
		return nil, err
	}
	return &BufferedPipelineHandler{q: q}, nil
}

// HandlePipelineEvent queues evt for asynchronous delivery.
func (h *BufferedPipelineHandler) HandlePipelineEvent(evt PipelineEvent) { h.q.push(evt) }

// Dropped returns the number of events discarded so far: overflow drops plus
// unprotected events submitted after Close. Every submitted event is either
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
