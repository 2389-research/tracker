// ABOUTME: Agent session that runs the agentic loop: LLM call -> tool execution -> loop.
// ABOUTME: Manages conversation state, tool dispatch, event emission, and result collection.
package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/agent/tools"
	"github.com/2389-research/tracker/llm"
)

// Completer is the interface needed from the LLM client.
type Completer interface {
	Complete(ctx context.Context, req *llm.Request) (*llm.Response, error)
}

// SessionOption configures a Session.
type SessionOption func(*Session)

// WithEventHandler attaches an event handler to receive session lifecycle events.
func WithEventHandler(h EventHandler) SessionOption {
	return func(s *Session) {
		s.handler = h
	}
}

// WithTools registers additional tools into the session's tool registry.
func WithTools(tt ...tools.Tool) SessionOption {
	return func(s *Session) {
		for _, t := range tt {
			s.registry.Register(t)
		}
	}
}

// WithEnvironment sets the execution environment and registers built-in tools.
func WithEnvironment(env exec.ExecutionEnvironment) SessionOption {
	return func(s *Session) {
		s.env = env
	}
}

// WithSessionRunner sets the session runner used by the spawn_agent tool to create child sessions.
func WithSessionRunner(runner tools.SessionRunner) SessionOption {
	return func(s *Session) {
		s.sessionRunner = runner
	}
}

// Session holds the state for a single agent conversation loop.
// A Session is single-use: Run must only be called once.
type Session struct {
	client          Completer
	config          SessionConfig
	handler         EventHandler
	registry        *tools.Registry
	env             exec.ExecutionEnvironment
	sessionRunner   tools.SessionRunner
	steering        <-chan string
	messages        []llm.Message
	id              string
	ran             bool
	cache           *toolCache
	lastCompactTurn int
	toolTimings     map[string]time.Duration
}

// ID returns the session's unique identifier.
func (s *Session) ID() string {
	return s.id
}

// NewSession creates a new agent session with the given LLM client, config, and options.
// Returns an error if the config is invalid.
func NewSession(client Completer, config SessionConfig, opts ...SessionOption) (*Session, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid session config: %w", err)
	}
	s := &Session{
		client:      client,
		config:      config,
		handler:     NoopHandler,
		registry:    tools.NewRegistry(),
		id:          generateSessionID(),
		toolTimings: make(map[string]time.Duration),
	}

	for _, opt := range opts {
		opt(s)
	}
	s.registry.SetOutputLimits(s.config.ToolOutputLimits)

	s.registerBuiltinTools()
	s.initToolCache()
	s.registerSpawnTool()

	return s, nil
}

// registerBuiltinTools registers built-in tools for the session environment.
// Custom tools registered via WithTools take precedence over built-ins.
func (s *Session) registerBuiltinTools() {
	if s.env == nil {
		return
	}
	for _, t := range builtInToolsForConfig(s.config, s.env) {
		if s.registry.Get(t.Name()) == nil {
			s.registry.Register(t)
		}
	}
}

// initToolCache initializes the tool result cache when enabled by config.
func (s *Session) initToolCache() {
	if s.config.CacheToolResults {
		s.cache = newToolCache()
	}
}

// registerSpawnTool registers the spawn_agent tool when a session runner is set.
func (s *Session) registerSpawnTool() {
	if s.sessionRunner == nil {
		return
	}
	spawnTool := tools.NewSpawnAgentTool(s.sessionRunner)
	if s.registry.Get(spawnTool.Name()) == nil {
		s.registry.Register(spawnTool)
	}
}

// turnState carries per-loop mutable state for the agentic turn loop.
type turnState struct {
	lastToolSignature    string
	consecutiveLoopCount int
	emptyResponseRetries int
}

// Run executes the agentic loop: send user input to the LLM, execute any tool
// calls, feed results back, and repeat until the LLM stops or max turns is reached.
func (s *Session) Run(ctx context.Context, userInput string) (SessionResult, error) {
	if s.ran {
		return SessionResult{}, fmt.Errorf("session already used; create a new Session for each Run call")
	}
	s.ran = true

	start := time.Now()
	tracker := NewContextWindowTracker(s.config.ContextWindowLimit, s.config.ContextWindowWarningThreshold)

	result := SessionResult{
		SessionID: s.id,
		ToolCalls: make(map[string]int),
	}

	s.emit(Event{Type: EventSessionStart, SessionID: s.id})
	defer func() {
		// Finalize cache stats on every exit path.
		if s.cache != nil {
			result.ToolCacheHits = s.cache.hits
			result.ToolCacheMisses = s.cache.misses
		}
		s.emit(Event{Type: EventSessionEnd, SessionID: s.id})
	}()

	s.initConversation(userInput)

	stoppedNaturally, err := s.runTurnLoop(ctx, start, tracker, &result)
	if err != nil {
		return result, err
	}

	if !stoppedNaturally {
		result.MaxTurnsUsed = true
	}

	result.ToolTimings = s.toolTimings
	result.ContextUtilization = tracker.Utilization()
	result.Duration = time.Since(start)
	return result, nil
}

// runTurnLoop executes the agentic loop and returns (stoppedNaturally, error).
func (s *Session) runTurnLoop(ctx context.Context, start time.Time, tracker *ContextWindowTracker, result *SessionResult) (bool, error) {
	ts := &turnState{}
	for turn := 1; turn <= s.config.MaxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			result.Error = err
			result.Duration = time.Since(start)
			return false, err
		}
		done, stop, err := s.executeTurn(ctx, turn, start, tracker, result, ts)
		if err != nil {
			return false, err
		}
		if stop {
			return done, nil
		}
	}
	return false, nil
}

// executeTurn runs one LLM turn and handles its outcome.
// Returns (stoppedNaturally, shouldStop, error).
func (s *Session) executeTurn(ctx context.Context, turn int, start time.Time, tracker *ContextWindowTracker, result *SessionResult, ts *turnState) (bool, bool, error) {
	s.drainSteering()
	s.emit(Event{Type: EventTurnStart, SessionID: s.id, Turn: turn})
	turnStart := time.Now()

	resp, err := s.doLLMCall(ctx, turn)
	if err != nil {
		result.Error = err
		result.Duration = time.Since(start)
		s.emit(Event{Type: EventError, SessionID: s.id, Err: err})
		return false, true, err
	}

	s.updateUsage(result, resp, turn, tracker)
	prevCacheHits, prevCacheMisses := s.snapshotCacheStats()
	s.messages = append(s.messages, resp.Message)

	toolCalls := resp.ToolCalls()
	if len(toolCalls) == 0 {
		done, stop, err := s.handleNoTools(resp, turn, turnStart, tracker, prevCacheHits, prevCacheMisses, result, ts, start)
		return done, stop, err
	}

	stopped := s.handleToolCalls(ctx, toolCalls, resp, turn, turnStart, tracker, prevCacheHits, prevCacheMisses, result, ts)
	if stopped {
		return false, true, nil
	}
	return false, false, nil
}

// handleNoTools processes a turn where the LLM returned no tool calls.
// Returns (stoppedNaturally, shouldStop, error).
func (s *Session) handleNoTools(resp *llm.Response, turn int, turnStart time.Time, tracker *ContextWindowTracker, prevCacheHits, prevCacheMisses int, result *SessionResult, ts *turnState, start time.Time) (bool, bool, error) {
	const maxEmptyResponseRetries = 2
	done := s.handleNoToolCalls(resp, turn, turnStart, tracker, prevCacheHits, prevCacheMisses, result)
	if !done {
		return false, false, nil
	}
	// Check for empty API response — retry before stopping.
	if result.TotalToolCalls() == 0 && len(resp.Message.Content) == 0 && resp.Usage.OutputTokens == 0 {
		if ts.emptyResponseRetries < maxEmptyResponseRetries {
			ts.emptyResponseRetries++
			diag := fmt.Sprintf("empty API response (0 output tokens, 0 tool calls) — provider=%s model=%s finish=%s input_tokens=%d raw_len=%d, retrying",
				resp.Provider, resp.Model, resp.FinishReason.Raw, resp.Usage.InputTokens, len(resp.Raw))
			s.emit(Event{Type: EventError, SessionID: s.id, Text: diag})
			s.messages = append(s.messages, llm.UserMessage(
				"Your previous response was empty. Please provide your response now.",
			))
			return false, false, nil // continue loop
		}
		emptyErr := fmt.Errorf("agent session failed: %d consecutive empty API responses", maxEmptyResponseRetries)
		result.Error = emptyErr
		result.Duration = time.Since(start)
		s.emit(Event{Type: EventError, SessionID: s.id, Err: emptyErr})
		return false, true, emptyErr
	}
	return true, true, nil // stoppedNaturally=true
}

// handleToolCalls processes a turn where the LLM returned tool calls.
// Returns true if the loop should stop (loop detected).
func (s *Session) handleToolCalls(ctx context.Context, toolCalls []llm.ToolCallData, resp *llm.Response, turn int, turnStart time.Time, tracker *ContextWindowTracker, prevCacheHits, prevCacheMisses int, result *SessionResult, ts *turnState) bool {
	signature := s.computeToolSignature(toolCalls)
	if signature == ts.lastToolSignature {
		ts.consecutiveLoopCount++
	} else {
		ts.lastToolSignature = signature
		ts.consecutiveLoopCount = 1
	}

	if ts.consecutiveLoopCount >= s.config.LoopDetectionThreshold {
		loopErr := fmt.Errorf("loop detected: same tool calls repeated %d times", ts.consecutiveLoopCount)
		s.emit(Event{Type: EventError, SessionID: s.id, Err: loopErr})
		result.LoopDetected = true
		s.emitTurnMetrics(turn, turnStart, resp, tracker, prevCacheHits, prevCacheMisses, result)
		s.emit(Event{Type: EventTurnEnd, SessionID: s.id, Turn: turn})
		return true
	}

	s.executeToolCalls(ctx, toolCalls, result)
	s.emitTurnMetrics(turn, turnStart, resp, tracker, prevCacheHits, prevCacheMisses, result)
	s.emit(Event{Type: EventTurnEnd, SessionID: s.id, Turn: turn})
	return false
}

// emit sends an event with the current timestamp to the session's event handler.
func (s *Session) emit(evt Event) {
	evt.Timestamp = time.Now()
	s.handler.HandleEvent(evt)
}

// emitTurnMetrics emits an EventTurnMetrics event and updates LongestTurn on result.
// It computes per-turn cache deltas from the snapshot taken before tool execution.
func (s *Session) emitTurnMetrics(turn int, turnStart time.Time, resp *llm.Response, tracker *ContextWindowTracker, prevCacheHits, prevCacheMisses int, result *SessionResult) {
	turnDuration := time.Since(turnStart)
	if turnDuration > result.LongestTurn {
		result.LongestTurn = turnDuration
	}

	turnCacheHits, turnCacheMisses := 0, 0
	if s.cache != nil {
		turnCacheHits = s.cache.hits - prevCacheHits
		turnCacheMisses = s.cache.misses - prevCacheMisses
	}

	cacheRead, cacheWrite := 0, 0
	if resp.Usage.CacheReadTokens != nil {
		cacheRead = *resp.Usage.CacheReadTokens
	}
	if resp.Usage.CacheWriteTokens != nil {
		cacheWrite = *resp.Usage.CacheWriteTokens
	}

	estimatedCost := resp.Usage.EstimatedCost
	if estimatedCost == 0 {
		estimatedCost = llm.EstimateCost(s.config.Model, resp.Usage)
	}

	s.emit(Event{
		Type:      EventTurnMetrics,
		SessionID: s.id,
		Turn:      turn,
		Metrics: &TurnMetrics{
			InputTokens:        resp.Usage.InputTokens,
			OutputTokens:       resp.Usage.OutputTokens,
			CacheReadTokens:    cacheRead,
			CacheWriteTokens:   cacheWrite,
			ContextUtilization: tracker.Utilization(),
			ToolCacheHits:      turnCacheHits,
			ToolCacheMisses:    turnCacheMisses,
			TurnDuration:       turnDuration,
			EstimatedCost:      estimatedCost,
		},
	})
}

func (s *Session) emitLLMTraceEvent(turn int, traceEvt llm.TraceEvent) {
	evt := Event{
		SessionID:     s.id,
		Turn:          turn,
		Provider:      traceEvt.Provider,
		Model:         traceEvt.Model,
		Preview:       traceEvt.Preview,
		ToolName:      traceEvt.ToolName,
		ProviderEvent: traceEvt.ProviderEvent,
		FinishReason:  traceEvt.FinishReason,
		Usage:         traceEvt.Usage,
	}

	switch traceEvt.Kind {
	case llm.TraceRequestStart:
		evt.Type = EventLLMRequestStart
	case llm.TraceReasoning:
		evt.Type = EventLLMReasoning
	case llm.TraceText:
		evt.Type = EventLLMText
	case llm.TraceToolPrepare:
		evt.Type = EventLLMToolPrepare
	case llm.TraceFinish:
		evt.Type = EventLLMFinish
	case llm.TraceProviderRaw:
		evt.Type = EventLLMProviderRaw
	default:
		return
	}

	s.emit(evt)
}

// boolToErrStr converts a boolean error flag to a string for event reporting.
func boolToErrStr(isErr bool) string {
	if isErr {
		return "true"
	}
	return ""
}

// generateSessionID creates a short random hex identifier for a session.
func generateSessionID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "0000"
	}
	return hex.EncodeToString(b)
}
