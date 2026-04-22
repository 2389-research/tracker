// ABOUTME: Manager loop handler — supervisor that launches a child pipeline asynchronously and polls until completion.
// ABOUTME: Implements the Attractor spec 4.11 observe+wait loop with configurable poll interval and max cycles.
package handlers

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

// childJoinGrace is the maximum time the manager will wait for a child
// goroutine to finish after cancellation. If the child is stuck in a
// non-context-aware handler, the manager returns after this grace period
// rather than blocking indefinitely (the child goroutine becomes orphaned).
const childJoinGrace = 30 * time.Second

// waitForChild waits for the child goroutine to send on resultCh, with a
// bounded grace period. Returns true if the child exited, false if the
// grace period expired.
func waitForChild(resultCh <-chan engineResultMsg) bool {
	select {
	case <-resultCh:
		return true
	case <-time.After(childJoinGrace):
		return false
	}
}

// ManagerLoopHandler supervises a child pipeline by launching it asynchronously
// and polling at intervals until the child completes or max cycles is reached.
type ManagerLoopHandler struct {
	graphs          map[string]*pipeline.Graph
	registry        *pipeline.HandlerRegistry
	pipelineEvents  pipeline.PipelineEventHandler
	registryFactory pipeline.RegistryFactory
}

// NewManagerLoopHandler creates a manager loop handler. All arguments may be nil;
// Execute will return clear errors when required dependencies are missing.
func NewManagerLoopHandler(
	graphs map[string]*pipeline.Graph,
	registry *pipeline.HandlerRegistry,
	pipelineEvents pipeline.PipelineEventHandler,
	factory pipeline.RegistryFactory,
) *ManagerLoopHandler {
	if pipelineEvents == nil {
		pipelineEvents = pipeline.PipelineNoopHandler
	}
	return &ManagerLoopHandler{
		graphs:          graphs,
		registry:        registry,
		pipelineEvents:  pipelineEvents,
		registryFactory: factory,
	}
}

func (h *ManagerLoopHandler) Name() string { return "stack.manager_loop" }

// managerLoopConfig holds parsed node attributes for the manager loop.
type managerLoopConfig struct {
	subgraphRef   string
	pollInterval  time.Duration
	maxCycles     int
	stopCondition string            // condition expression evaluated each tick
	steerExpr     string            // condition that triggers steering injection
	steerKeys     map[string]string // key-value pairs injected when steerExpr matches
}

// parseManagerLoopConfig extracts manager loop configuration from node attributes.
//
// Two attr namings are supported: the unprefixed DOT-export contract used by
// dippin-lang v0.22.0+ (`poll_interval`, `max_cycles`, `stop_condition`,
// `steer_condition`, `steer_context`) and the legacy `manager.*` prefixed
// variants authored directly in DOT before the IR migration. When both are
// present the unprefixed form wins — it is the authoritative contract going
// forward, so a migrated pipeline with leftover `manager.*` attrs still gets
// the new values.
func parseManagerLoopConfig(attrs map[string]string) (managerLoopConfig, error) {
	cfg := managerLoopConfig{
		pollInterval: 45 * time.Second,
		maxCycles:    1000,
	}

	cfg.subgraphRef = attrs["subgraph_ref"]
	if cfg.subgraphRef == "" {
		return cfg, fmt.Errorf("manager_loop: missing required attribute \"subgraph_ref\"")
	}

	if v := managerAttr(attrs, "poll_interval"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return cfg, fmt.Errorf("manager_loop: invalid poll_interval %q: %w", v, err)
		}
		if d <= 0 {
			return cfg, fmt.Errorf("manager_loop: poll_interval must be > 0, got %q", v)
		}
		cfg.pollInterval = d
	}

	if v := managerAttr(attrs, "max_cycles"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return cfg, fmt.Errorf("manager_loop: invalid max_cycles %q: %w", v, err)
		}
		if n <= 0 {
			return cfg, fmt.Errorf("manager_loop: max_cycles must be > 0, got %q", v)
		}
		cfg.maxCycles = n
	}

	cfg.stopCondition = managerAttr(attrs, "stop_condition")
	cfg.steerExpr = managerAttr(attrs, "steer_condition")
	cfg.steerKeys = parseSteerContext(managerAttr(attrs, "steer_context"))

	// Both sides of steering must be set together or neither — a condition
	// without a context map is inert (nothing to inject) and a context map
	// without a condition never fires. Either case is almost certainly an
	// author mistake, so reject at parse time rather than silently producing
	// a no-op supervisor (violates CLAUDE.md "never silently swallow errors").
	if cfg.steerExpr != "" && len(cfg.steerKeys) == 0 {
		return cfg, fmt.Errorf("manager_loop: steer_condition is set but steer_context is empty — nothing to inject")
	}
	if cfg.steerExpr == "" && len(cfg.steerKeys) > 0 {
		return cfg, fmt.Errorf("manager_loop: steer_context is set but steer_condition is empty — no trigger for injection")
	}

	return cfg, nil
}

// managerAttr looks up a manager_loop attribute, preferring the unprefixed
// dippin-lang v0.22.0 contract key and falling back to the legacy
// "manager."+key form so hand-authored DOT files keep working.
func managerAttr(attrs map[string]string, key string) string {
	if v := attrs[key]; v != "" {
		return v
	}
	return attrs["manager."+key]
}

// steerContextDecoder reverses the encoder in pipeline/dippin_adapter.go
// (which mirrors dippin-lang v0.22.0 export.flattenSteerContext). Sequences
// are listed longest-first so the replacer matches greedily.
var steerContextDecoder = strings.NewReplacer(
	"%25", "%",
	"%2C", ",",
	"%3D", "=",
)

// decodeSteerContextToken reverses encodeSteerContextToken. Returns the input
// unchanged when it contains no percent-encoded sequences.
func decodeSteerContextToken(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	return steerContextDecoder.Replace(s)
}

// parseSteerContext parses a comma-separated "key=value,key=value" string into
// a map. Reserved characters (',', '=', '%') in keys or values appear as
// percent-encoded tokens (`%2C`, `%3D`, `%25`) and are decoded back to their
// originals — see flattenSteerContext in pipeline/dippin_adapter.go.
// Empty input returns nil. Malformed pairs are silently skipped, matching
// dippin-lang's migrate.parseFlattenedSteerContext behavior.
func parseSteerContext(s string) map[string]string {
	if s == "" {
		return nil
	}
	result := make(map[string]string)
	for _, pair := range strings.Split(s, ",") {
		parts := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(parts) == 2 {
			k := decodeSteerContextToken(strings.TrimSpace(parts[0]))
			v := decodeSteerContextToken(strings.TrimSpace(parts[1]))
			result[k] = v
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// engineResultMsg carries the result from the child engine goroutine.
type engineResultMsg struct {
	result *pipeline.EngineResult
	err    error
}

// Execute runs the manager loop: launches a child pipeline in a goroutine,
// polls at intervals, and returns when the child completes or limits are hit.
func (h *ManagerLoopHandler) Execute(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
	cfg, err := parseManagerLoopConfig(node.Attrs)
	if err != nil {
		return pipeline.Outcome{Status: pipeline.OutcomeFail}, err
	}

	// Look up the child graph.
	if h.graphs == nil {
		return pipeline.Outcome{Status: pipeline.OutcomeFail},
			fmt.Errorf("manager_loop: no subgraphs available, cannot find %q", cfg.subgraphRef)
	}
	childGraph, ok := h.graphs[cfg.subgraphRef]
	if !ok {
		return pipeline.Outcome{Status: pipeline.OutcomeFail},
			fmt.Errorf("manager_loop: subgraph %q not found", cfg.subgraphRef)
	}

	// Build child engine with scoped events, matching SubgraphHandler pattern.
	scopedPipeline := pipeline.NodeScopedPipelineHandler(node.ID, h.pipelineEvents)
	childRegistry := h.registry
	if h.registryFactory != nil {
		childRegistry = h.registryFactory(childGraph, node.ID)
	}
	// Defensive: if both registry and factory are nil we'd pass a nil
	// registry to NewEngine and panic on the first handler lookup.
	// Report clearly instead.
	if childRegistry == nil {
		return pipeline.Outcome{Status: pipeline.OutcomeFail},
			fmt.Errorf("manager_loop: no handler registry available for child subgraph %q", cfg.subgraphRef)
	}

	childCtx, cancelChild := context.WithCancel(ctx)
	defer cancelChild()

	// Create steering channel if steering is configured.
	var steeringCh chan map[string]string
	if cfg.steerExpr != "" && cfg.steerKeys != nil {
		steeringCh = make(chan map[string]string, 1)
	}

	engineOpts := []pipeline.EngineOption{
		pipeline.WithInitialContext(pctx.Snapshot()),
		pipeline.WithPipelineEventHandler(scopedPipeline),
	}
	if steeringCh != nil {
		engineOpts = append(engineOpts, pipeline.WithSteeringChan(steeringCh))
	}
	engine := pipeline.NewEngine(childGraph, childRegistry, engineOpts...)

	// Launch child pipeline in a goroutine.
	resultCh := make(chan engineResultMsg, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				resultCh <- engineResultMsg{
					err: fmt.Errorf("panic in manager_loop child %q: %v", cfg.subgraphRef, r),
				}
			}
		}()
		result, runErr := engine.Run(childCtx)
		resultCh <- engineResultMsg{result: result, err: runErr}
	}()

	// Emit child-started event. Handler-emitted events deliberately
	// leave RunID unset — it is not surfaced to handlers through
	// PipelineContext today. Observability tools should correlate via
	// NodeID + Timestamp for now.
	h.pipelineEvents.HandlePipelineEvent(pipeline.PipelineEvent{
		Type:      pipeline.EventStageStarted,
		Timestamp: time.Now(),
		NodeID:    node.ID,
		Message:   fmt.Sprintf("manager_loop: child %q launched", cfg.subgraphRef),
	})
	pctx.Set("stack.child.status", "running")

	// Poll loop. Using an explicit time.NewTimer (rather than time.After
	// inside the select) so we can Stop+Reset it per iteration. time.After
	// allocates a new timer per call that isn't GC'd until it fires; with
	// short poll intervals in long-running loops, those accumulate.
	pollTimer := time.NewTimer(cfg.pollInterval)
	defer pollTimer.Stop()
	cycles := 0
	for {
		select {
		case <-ctx.Done():
			cancelChild()
			waitForChild(resultCh)
			pctx.Set("stack.child.status", "cancelled")
			h.pipelineEvents.HandlePipelineEvent(pipeline.PipelineEvent{
				Type:      pipeline.EventStageFailed,
				Timestamp: time.Now(),
				NodeID:    node.ID,
				Message:   fmt.Sprintf("manager_loop: cancelled: %v", ctx.Err()),
			})
			return pipeline.Outcome{Status: pipeline.OutcomeFail},
				fmt.Errorf("manager_loop: cancelled: %w", ctx.Err())

		case msg := <-resultCh:
			return h.handleChildResult(node.ID, msg, cycles, pctx)

		case <-pollTimer.C:
			// If the child's result became ready concurrently with this
			// tick, prefer completion — select among ready cases is
			// nondeterministic, so without this check a tick could win
			// the race and trigger max_cycles failure even when the
			// child already finished.
			select {
			case msg := <-resultCh:
				return h.handleChildResult(node.ID, msg, cycles, pctx)
			default:
			}

			cycles++
			pctx.Set("stack.child.cycles", strconv.Itoa(cycles))

			h.pipelineEvents.HandlePipelineEvent(pipeline.PipelineEvent{
				Type:      pipeline.EventManagerCycleTick,
				Timestamp: time.Now(),
				NodeID:    node.ID,
				Message:   fmt.Sprintf("manager_loop: cycle %d/%d", cycles, cfg.maxCycles),
			})

			if cycles >= cfg.maxCycles {
				cancelChild()
				waitForChild(resultCh)
				pctx.Set("stack.child.status", "max_cycles_exceeded")
				h.pipelineEvents.HandlePipelineEvent(pipeline.PipelineEvent{
					Type:      pipeline.EventStageFailed,
					Timestamp: time.Now(),
					NodeID:    node.ID,
					Message:   fmt.Sprintf("manager_loop: max_cycles %d reached", cfg.maxCycles),
				})
				return pipeline.Outcome{Status: pipeline.OutcomeFail},
					fmt.Errorf("manager_loop: max_cycles %d reached", cfg.maxCycles)
			}

			// Evaluate stop condition against the parent context. A parse
			// error here means the author wrote a malformed condition —
			// fail the manager loop with a clear error rather than
			// silently treating as "never match", which would hide the
			// misconfiguration until max_cycles.
			if cfg.stopCondition != "" {
				match, condErr := pipeline.EvaluateCondition(cfg.stopCondition, pctx)
				if condErr != nil {
					cancelChild()
					waitForChild(resultCh)
					pctx.Set("stack.child.status", "stop_condition_invalid")
					h.pipelineEvents.HandlePipelineEvent(pipeline.PipelineEvent{
						Type:      pipeline.EventStageFailed,
						Timestamp: time.Now(),
						NodeID:    node.ID,
						Message:   fmt.Sprintf("manager_loop: stop_condition %q is invalid: %v", cfg.stopCondition, condErr),
					})
					return pipeline.Outcome{Status: pipeline.OutcomeFail},
						fmt.Errorf("manager_loop: stop_condition %q is invalid: %w", cfg.stopCondition, condErr)
				}
				if match {
					cancelChild()
					waitForChild(resultCh)
					pctx.Set("stack.child.status", "stop_condition_met")
					h.pipelineEvents.HandlePipelineEvent(pipeline.PipelineEvent{
						Type:      pipeline.EventStageCompleted,
						Timestamp: time.Now(),
						NodeID:    node.ID,
						Message:   fmt.Sprintf("manager_loop: stop_condition met after %d cycles", cycles),
					})
					return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
				}
			}

			// Steering: inject context into running child when condition matches.
			if cfg.steerExpr != "" && steeringCh != nil {
				match, condErr := pipeline.EvaluateCondition(cfg.steerExpr, pctx)
				if condErr != nil {
					cancelChild()
					waitForChild(resultCh)
					pctx.Set("stack.child.status", "steer_condition_invalid")
					h.pipelineEvents.HandlePipelineEvent(pipeline.PipelineEvent{
						Type:      pipeline.EventStageFailed,
						Timestamp: time.Now(),
						NodeID:    node.ID,
						Message:   fmt.Sprintf("manager_loop: steer_condition %q is invalid: %v", cfg.steerExpr, condErr),
					})
					return pipeline.Outcome{Status: pipeline.OutcomeFail},
						fmt.Errorf("manager_loop: steer_condition %q is invalid: %w", cfg.steerExpr, condErr)
				}
				if match {
					select {
					case steeringCh <- cfg.steerKeys:
						h.pipelineEvents.HandlePipelineEvent(pipeline.PipelineEvent{
							Type:      pipeline.EventManagerCycleTick,
							Timestamp: time.Now(),
							NodeID:    node.ID,
							Message:   fmt.Sprintf("manager_loop: steered %d keys into child", len(cfg.steerKeys)),
						})
					default:
						// Channel full — child hasn't drained yet. Skip this cycle.
					}
				}
			}
			// Reset for the next poll. The timer is already drained by the
			// case firing above, so Reset is safe here.
			pollTimer.Reset(cfg.pollInterval)
		}
	}
}

// handleChildResult processes the child engine's result and returns the appropriate outcome.
// The engine may return both a result and an error (e.g. strict failure edges), so we
// prioritize the result when available over treating the error as a bare crash.
func (h *ManagerLoopHandler) handleChildResult(nodeID string, msg engineResultMsg, cycles int, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
	pctx.Set("stack.child.cycles", strconv.Itoa(cycles))

	// Engine may return both result + error (e.g. node failed with no failure edge).
	// When a result is available, use its status/context rather than treating as a crash.
	if msg.result != nil {
		result := msg.result
		if result.Status == pipeline.OutcomeSuccess {
			pctx.Set("stack.child.status", "success")
			pctx.Set("stack.child.exit_status", pipeline.OutcomeSuccess)
			h.pipelineEvents.HandlePipelineEvent(pipeline.PipelineEvent{
				Type:      pipeline.EventStageCompleted,
				Timestamp: time.Now(),
				NodeID:    nodeID,
				Message:   fmt.Sprintf("manager_loop: child completed successfully after %d cycles", cycles),
			})
			return pipeline.Outcome{
				Status:         pipeline.OutcomeSuccess,
				ContextUpdates: result.Context,
			}, nil
		}

		// Child pipeline failed (non-success status). Record the child's
		// real exit status (e.g. OutcomeBudgetExceeded) in context for
		// inspection, but return a valid handler-level outcome. Handler
		// Status values must be from the handler-outcome set
		// (success/fail/retry) — engine-level statuses like
		// OutcomeBudgetExceeded would fall through the engine's outcome
		// switch and be silently treated as success.
		childStatus := result.Status
		if childStatus == "" {
			childStatus = pipeline.OutcomeFail
		}
		pctx.Set("stack.child.status", "failed")
		pctx.Set("stack.child.exit_status", childStatus)
		h.pipelineEvents.HandlePipelineEvent(pipeline.PipelineEvent{
			Type:      pipeline.EventStageFailed,
			Timestamp: time.Now(),
			NodeID:    nodeID,
			Message:   fmt.Sprintf("manager_loop: child completed with status %q", childStatus),
		})
		return pipeline.Outcome{
			Status:         pipeline.OutcomeFail,
			ContextUpdates: result.Context,
		}, nil
	}

	// No result at all — child crashed or panicked before producing one.
	// Guarantee a non-nil error so callers never see (OutcomeFail, nil):
	// if the goroutine sent neither result nor err, synthesize one.
	err := msg.err
	if err == nil {
		err = fmt.Errorf("manager_loop: child exited with no result and no error")
	}
	pctx.Set("stack.child.status", "error")
	h.pipelineEvents.HandlePipelineEvent(pipeline.PipelineEvent{
		Type:      pipeline.EventStageFailed,
		Timestamp: time.Now(),
		NodeID:    nodeID,
		Message:   fmt.Sprintf("manager_loop: child error: %v", err),
	})
	return pipeline.Outcome{Status: pipeline.OutcomeFail}, err
}
