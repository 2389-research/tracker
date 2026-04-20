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
func parseManagerLoopConfig(attrs map[string]string) (managerLoopConfig, error) {
	cfg := managerLoopConfig{
		pollInterval: 45 * time.Second,
		maxCycles:    1000,
	}

	cfg.subgraphRef = attrs["subgraph_ref"]
	if cfg.subgraphRef == "" {
		return cfg, fmt.Errorf("manager_loop: missing required attribute \"subgraph_ref\"")
	}

	if v := attrs["manager.poll_interval"]; v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.pollInterval = d
		}
	}

	if v := attrs["manager.max_cycles"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.maxCycles = n
		}
	}

	cfg.stopCondition = attrs["manager.stop_condition"]
	cfg.steerExpr = attrs["manager.steer_condition"]
	cfg.steerKeys = parseSteerContext(attrs["manager.steer_context"])

	return cfg, nil
}

// parseSteerContext parses a comma-separated "key=value,key=value" string into a map.
// Empty input returns nil.
func parseSteerContext(s string) map[string]string {
	if s == "" {
		return nil
	}
	result := make(map[string]string)
	for _, pair := range strings.Split(s, ",") {
		parts := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(parts) == 2 {
			result[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
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

	// Emit child-started event.
	h.pipelineEvents.HandlePipelineEvent(pipeline.PipelineEvent{
		Type:      pipeline.EventStageStarted,
		Timestamp: time.Now(),
		NodeID:    node.ID,
		Message:   fmt.Sprintf("manager_loop: child %q launched", cfg.subgraphRef),
	})
	pctx.Set("stack.child.status", "running")

	// Poll loop.
	cycles := 0
	for {
		select {
		case <-ctx.Done():
			cancelChild()
			pctx.Set("stack.child.status", "cancelled")
			return pipeline.Outcome{Status: pipeline.OutcomeFail},
				fmt.Errorf("manager_loop: cancelled: %w", ctx.Err())

		case msg := <-resultCh:
			return h.handleChildResult(node.ID, msg, cycles, pctx)

		case <-time.After(cfg.pollInterval):
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

			// Evaluate stop condition against the parent context.
			if cfg.stopCondition != "" {
				if match, _ := pipeline.EvaluateCondition(cfg.stopCondition, pctx); match {
					cancelChild()
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
				if match, _ := pipeline.EvaluateCondition(cfg.steerExpr, pctx); match {
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

		// Child pipeline failed (non-success status).
		pctx.Set("stack.child.status", "failed")
		pctx.Set("stack.child.exit_status", result.Status)
		h.pipelineEvents.HandlePipelineEvent(pipeline.PipelineEvent{
			Type:      pipeline.EventStageFailed,
			Timestamp: time.Now(),
			NodeID:    nodeID,
			Message:   fmt.Sprintf("manager_loop: child completed with status %q", result.Status),
		})
		return pipeline.Outcome{
			Status:         pipeline.OutcomeFail,
			ContextUpdates: result.Context,
		}, nil
	}

	// No result at all — child crashed or panicked before producing one.
	pctx.Set("stack.child.status", "error")
	h.pipelineEvents.HandlePipelineEvent(pipeline.PipelineEvent{
		Type:      pipeline.EventStageFailed,
		Timestamp: time.Now(),
		NodeID:    nodeID,
		Message:   fmt.Sprintf("manager_loop: child error: %v", msg.err),
	})
	return pipeline.Outcome{Status: pipeline.OutcomeFail}, msg.err
}
