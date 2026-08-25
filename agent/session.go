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

// Completer is the interface needed from the LLM client. It's an alias of
// tools.Completer so both packages refer to the same type — preventing
// silent divergence if either side grows new methods.
type Completer = tools.Completer

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
	episodeLog      EpisodeLog
	// resumeTurn is the last completed turn restored from a TurnSnapshot (#427).
	// Zero means a fresh run; >0 makes the turn loop start at resumeTurn+1 and
	// tells Run to skip conversation init (messages are already seeded).
	resumeTurn int
	// resumeProgress carries the accounting/control-flow state restored from a
	// TurnSnapshot (#596), applied to result/tracker/turnState at the top of the
	// turn loop. Nil on a fresh run or a pre-#596 (v1) snapshot.
	resumeProgress *SessionProgress
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
	s.registerStatusTool()

	// tool_access enforcement (issue #258): after every registration path
	// (built-ins, WithTools, spawn_agent), clear the registry if access is
	// restricted. Defense in depth — builtInToolsForConfig already returns
	// nil under restriction, but WithTools and the spawn tool bypass that
	// guard. The empty registry plus ToolChoice=none on the request gives
	// fail-closed behavior even if a caller mis-orders WithTools.
	if s.config.IsToolAccessRestricted() {
		s.registry = tools.NewRegistry()
		s.registry.SetOutputLimits(s.config.ToolOutputLimits)
	}

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

// reflectionPrompt is injected as a user message after a turn where one or more
// tool calls failed.  It structures the LLM's reasoning about the failure before
// it retries.  Keep it short — the actual error details live in the tool result
// messages that precede it.
const reflectionPrompt = `One or more tool calls failed. Before your next action, briefly analyze:
1. What specifically went wrong?
2. What assumption was incorrect?
3. What is the minimal change that will fix this?

Then proceed with your corrective action.`

// maxReflectionTurns is the maximum number of consecutive turns on which the
// reflection prompt is injected.  After this cap the agent retries without the
// extra prompt to avoid infinite reflection loops.
const maxReflectionTurns = 3

// turnState carries per-loop mutable state for the agentic turn loop.
type turnState struct {
	lastToolSignature      string
	consecutiveLoopCount   int
	emptyResponseRetries   int
	consecutiveReflected   int  // turns in a row that triggered reflection
	consecutiveNoToolTurns int  // #304: turns in a row with no progress (no-progress detector)
	turnEdited             bool // #531: this turn landed a successful edit
	sawEdit                bool // #531: sticky — the session has landed ≥1 edit
}

// Run executes the agentic loop: send user input to the LLM, execute any tool
// calls, feed results back, and repeat until the LLM stops or max turns is reached.
func (s *Session) Run(ctx context.Context, userInput string) (result SessionResult, err error) {
	if s.ran {
		return SessionResult{}, fmt.Errorf("session already used; create a new Session for each Run call")
	}
	s.ran = true

	start := time.Now()
	tracker := NewContextWindowTracker(s.config.EffectiveContextWindowLimit(), s.config.ContextWindowWarningThreshold)

	result = SessionResult{
		SessionID: s.id,
		Provider:  s.config.Provider,
		ToolCalls: make(map[string]int),
	}

	s.emit(Event{Type: EventSessionStart, SessionID: s.id})
	defer func() {
		// Finalize cache stats on every exit path — named returns make this reach the caller (#618).
		if s.cache != nil {
			result.ToolCacheHits = s.cache.hits
			result.ToolCacheMisses = s.cache.misses
		}
		s.emit(Event{Type: EventSessionEnd, SessionID: s.id})
	}()

	// #427: when resuming from a durable turn snapshot the conversation is
	// already seeded (messages + episode log restored), so skip fresh init and
	// the one-shot planning turn — both would corrupt the restored history.
	if !s.maybeRestoreTurnCheckpoint() {
		s.initConversation(ctx, userInput)
		if err := s.maybeRunPlanningTurn(ctx, &result); err != nil {
			result.Duration = time.Since(start)
			result.Disposition = deriveDisposition(&result)
			return result, err
		}
	}

	stoppedNaturally, err := s.runTurnLoop(ctx, start, tracker, &result)
	if err != nil {
		result.Disposition = deriveDisposition(&result)
		return result, err
	}

	s.finalizeTurnExhaustion(ctx, stoppedNaturally, &result)

	result.ToolTimings = s.toolTimings
	result.ContextUtilization = tracker.Utilization()
	result.EpisodeSummary = s.episodeLog.Summary()
	result.Duration = time.Since(start)
	result.Disposition = deriveDisposition(&result)
	return result, nil
}

// finalizeTurnExhaustion records turn exhaustion on result and runs the #303
// verify-on-breach pass when the loop ran out of turns.
//
// #304: node-level guards (NodeCostExceeded, NoProgressDetected) stop the loop
// early and are not turn exhaustion — they skip MaxTurnsUsed and
// verify-on-breach so the codergen handler can route them correctly.
func (s *Session) finalizeTurnExhaustion(ctx context.Context, stoppedNaturally bool, result *SessionResult) {
	guardStop := result.NodeCostExceeded || result.NoProgressDetected
	if stoppedNaturally || guardStop {
		return
	}
	result.MaxTurnsUsed = true
	// #303 verify-on-breach: only on plain exhaustion (not a detected loop),
	// only when the pipeline asked for it via VerifyOnBreach, and only against
	// an explicit command. Reached only when runTurnLoop returned err==nil, so
	// a provider error is never masked.
	if s.config.VerifyOnBreach && !result.LoopDetected {
		result.BreachVerify = s.runBreachVerify(ctx)
	}
}

// runOneTurn executes a single turn and evaluates the post-turn halt guards.
// Returns (halt, stoppedNaturally, error).
func (s *Session) runOneTurn(ctx context.Context, turn int, start time.Time, tracker *ContextWindowTracker, result *SessionResult, ts *turnState) (bool, bool, error) {
	prevToolCount := result.TotalToolCalls()
	prevEmptyRetries := ts.emptyResponseRetries
	done, stop, err := s.executeTurn(ctx, turn, start, tracker, result, ts)
	if err != nil {
		return false, false, err
	}
	// #304: per-node cost ceiling — checked before honouring stop so the
	// ceiling takes precedence even on the turn that naturally completes
	// the session (cost updated by executeTurn before it returns).
	if s.config.MaxCostUSD > 0 && result.Usage.EstimatedCost > s.config.MaxCostUSD {
		result.NodeCostExceeded = true
		return true, false, nil
	}
	if stop {
		return true, done, nil
	}
	if s.noProgressBreached(result, ts, prevToolCount, prevEmptyRetries) {
		result.NoProgressDetected = true
		return true, false, nil
	}
	return false, false, nil
}

// noProgressBreached advances the #304 no-progress detector for the just-finished
// turn and reports whether it tripped: K consecutive no-progress turns.
// Empty-response retry sequences are skipped (recovering, not stuck). #531: once
// the agent has landed an edit (ts.sawEdit), "progress" keys on another edit — a
// new-commit / verify-state proxy — catching a tight-looping agent that calls
// tools every turn without advancing (the old raw tool-call count missed it).
// Before the first edit it falls back to the legacy tool-call heuristic.
func (s *Session) noProgressBreached(result *SessionResult, ts *turnState, prevToolCount, prevEmptyRetries int) bool {
	if s.config.NoProgressTurns <= 0 || ts.emptyResponseRetries != prevEmptyRetries {
		return false
	}
	progressed := ts.turnEdited
	if !ts.sawEdit {
		progressed = result.TotalToolCalls() > prevToolCount // fallback: no edit signal yet
	}
	if progressed {
		ts.consecutiveNoToolTurns = 0
		return false
	}
	ts.consecutiveNoToolTurns++
	return ts.consecutiveNoToolTurns >= s.config.NoProgressTurns
}

// executeTurn runs one LLM turn and handles its outcome.
// Returns (stoppedNaturally, shouldStop, error).
func (s *Session) executeTurn(ctx context.Context, turn int, start time.Time, tracker *ContextWindowTracker, result *SessionResult, ts *turnState) (bool, bool, error) {
	ts.turnEdited = false // #531: reset here (every path to noProgressBreached passes through)
	s.drainSteering()

	// Check if a turn-budget checkpoint should fire on this turn.
	if cpMsg := evalCheckpoint(s.config.Checkpoints, turn, s.config.MaxTurns); cpMsg != "" {
		s.messages = append(s.messages, llm.UserMessage(cpMsg))
		s.emit(Event{Type: EventCheckpoint, SessionID: s.id, Turn: turn, Text: cpMsg})
	}

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

	stop, naturally := s.handleToolCalls(ctx, toolCalls, resp, turn, turnStart, tracker, prevCacheHits, prevCacheMisses, result, ts)
	if stop {
		return naturally, true, nil
	}

	// Run the verify-after-edit loop if any edit tools were called this turn.
	if s.turnHasEdits(toolCalls) {
		ts.turnEdited = true // #531: the workspace advanced — the progress signal
		ts.sawEdit = true
		if err := s.runVerifyLoop(ctx, result); err != nil {
			// Verification infrastructure failure (e.g. binary not found).
			// Emit a warning and proceed — do not block the pipeline.
			s.emit(Event{Type: EventError, SessionID: s.id, Text: fmt.Sprintf("verify-after-edit: %v (proceeding)", err)})
		}
	}

	return false, false, nil
}

// handleNoTools processes a turn where the LLM returned no tool calls.
// Returns (stoppedNaturally, shouldStop, error).
func (s *Session) handleNoTools(resp *llm.Response, turn int, turnStart time.Time, tracker *ContextWindowTracker, prevCacheHits, prevCacheMisses int, result *SessionResult, ts *turnState, start time.Time) (bool, bool, error) {
	// A text-only turn is a clean turn — reset the reflection counter so that
	// a subsequent error gets the full three-turn reflection window again.
	ts.consecutiveReflected = 0
	const maxEmptyResponseRetries = 2
	done := s.handleNoToolCalls(resp, turn, turnStart, tracker, prevCacheHits, prevCacheMisses, result)
	if !done {
		return false, false, nil
	}
	// Check for empty API response — retry before stopping. This is decided on
	// the CURRENT response alone (0 content parts, 0 output tokens), NOT on
	// session-total tool calls (#601): an empty response after earlier tool work
	// is still an empty response and must fail loudly, never be accepted as a
	// clean stop.
	if len(resp.Message.Content) == 0 && resp.Usage.OutputTokens == 0 {
		if ts.emptyResponseRetries < maxEmptyResponseRetries {
			ts.emptyResponseRetries++
			diag := fmt.Sprintf("empty API response (0 output tokens, 0 tool calls) — provider=%s model=%s finish=%s input_tokens=%d raw_len=%d, retrying",
				resp.Provider, resp.Model, resp.FinishReason.Raw, resp.Usage.InputTokens, len(resp.Raw))
			s.emit(Event{Type: EventError, SessionID: s.id, Text: diag})
			s.dropTrailingEmptyAssistant() // #540: else it becomes a non-final empty-content msg (provider 400)
			s.messages = append(s.messages, llm.UserMessage(
				"Your previous response was empty. Please provide your response now.",
			))
			return false, false, nil // continue loop
		}
		// maxEmptyResponseRetries counts the retries; the total number of
		// consecutive empty responses observed is one more than that.
		emptyErr := &errEmptyResponse{count: maxEmptyResponseRetries + 1}
		result.Error = emptyErr
		result.Duration = time.Since(start)
		s.emit(Event{Type: EventError, SessionID: s.id, Err: emptyErr})
		return false, true, emptyErr
	}
	return true, true, nil // stoppedNaturally=true
}

// handleToolCalls processes a turn where the LLM returned tool calls.
// Returns:
//   - stop: the outer loop should break.
//   - naturally: the stop was a clean end (terminal-tool success), as opposed
//     to a loop-detection abort. Only meaningful when stop=true.
func (s *Session) handleToolCalls(ctx context.Context, toolCalls []llm.ToolCallData, resp *llm.Response, turn int, turnStart time.Time, tracker *ContextWindowTracker, prevCacheHits, prevCacheMisses int, result *SessionResult, ts *turnState) (stop, naturally bool) {
	// #507: fail closed on a length-truncated turn — its tool-call arguments
	// may be incomplete, so reject them and route recovery rather than dispatch.
	if s.rejectTruncatedToolCalls(resp, toolCalls, turn, turnStart, tracker, prevCacheHits, prevCacheMisses, result) {
		return false, false
	}
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
		return true, false
	}

	hadErrors, terminate := s.executeToolCalls(ctx, toolCalls, result, turn)
	s.maybeInjectReflection(hadErrors, ts)
	s.emitTurnMetrics(turn, turnStart, resp, tracker, prevCacheHits, prevCacheMisses, result)
	s.emit(Event{Type: EventTurnEnd, SessionID: s.id, Turn: turn})
	return terminate, terminate
}

// latestToolResultErrors maps toolCallID → isError for the most recent
// tool-result message. executeToolCalls appends that message before
// turnHasEdits is called, but maybeInjectReflection may append additional user
// messages afterwards — so scan backwards to find the most recent RoleTool
// message and stop there.
func (s *Session) latestToolResultErrors(sizeHint int) map[string]bool {
	errByID := make(map[string]bool, sizeHint)
	for i := len(s.messages) - 1; i >= 0; i-- {
		if s.messages[i].Role != llm.RoleTool {
			continue
		}
		collectToolResultErrors(errByID, s.messages[i].Content)
		break
	}
	return errByID
}

// collectToolResultErrors records toolCallID → isError for every tool-result
// part in parts.
func collectToolResultErrors(dst map[string]bool, parts []llm.ContentPart) {
	for _, part := range parts {
		if part.Kind == llm.KindToolResult && part.ToolResult != nil {
			dst[part.ToolResult.ToolCallID] = part.ToolResult.IsError
		}
	}
}

// turnHasEdits reports whether any edit tool call in the turn succeeded.
// It checks both the tool name and the corresponding tool result to ensure the
// workspace was actually modified. A failed write (e.g. permission denied)
// does not count as an edit — running verification after an unchanged workspace
// would test pre-existing failures unrelated to the current turn.
func (s *Session) turnHasEdits(toolCalls []llm.ToolCallData) bool {
	// Build a map of toolCallID → isError from the most recent tool-result message.
	// executeToolCalls appends this message before turnHasEdits is called, but
	// maybeInjectReflection may append additional user messages afterwards. Scan
	// backwards to find the most recent RoleTool message.
	errByID := s.latestToolResultErrors(len(toolCalls))
	for _, tc := range toolCalls {
		if !isEditTool(tc.Name) {
			continue
		}
		// Use the map-ok idiom: a missing ID (tool result not found) is not a
		// confirmed success and should not trigger verification.
		isErr, ok := errByID[tc.ID]
		if ok && !isErr {
			return true
		}
	}
	return false
}

// runBreachVerify runs a single verify pass after turn exhaustion and maps the
// result to a BreachVerifyState. A real execution error (binary missing, bad
// workdir — NOT a test failure) is surfaced via EventVerify and treated as
// Failed (non-green): per CLAUDE.md we never swallow it, and a breach must never
// advance on an unverifiable tree.
func (s *Session) runBreachVerify(ctx context.Context) BreachVerifyState {
	v := resolveBreachVerifier(s.config)
	if v == nil {
		return BreachVerifyNotRun
	}
	res, err := v.run(ctx)
	if err != nil {
		s.emit(Event{Type: EventVerify, SessionID: s.id, Text: fmt.Sprintf("verify-on-breach: execution error: %v", err)})
		return BreachVerifyFailed
	}
	if res.Passed {
		s.emit(Event{Type: EventVerify, SessionID: s.id, Text: fmt.Sprintf("verify-on-breach: passed (%s)", res.Command)})
		return BreachVerifyPassed
	}
	s.emit(Event{Type: EventVerify, SessionID: s.id, Text: fmt.Sprintf("verify-on-breach: failed (exit %d, %s)", res.ExitCode, res.Command)})
	return BreachVerifyFailed
}

// runVerifyLoop runs the verify-after-edit inner loop. It resolves the verify
// command, executes it, and injects repair prompts on failure. Repair turns
// do NOT count toward session MaxTurns. After MaxVerifyRetries failures the
// loop exits without blocking the caller.
func (s *Session) runVerifyLoop(ctx context.Context, result *SessionResult) error {
	v := newVerifier(s.config)
	if v == nil {
		return nil // disabled or no command detected
	}

	// MaxVerifyRetries == 0 means "run once, no retries on failure".
	// DefaultConfig sets 2; callers that want to disable repair entirely should
	// set MaxVerifyRetries to 0 explicitly and rely on the single-pass verify.
	maxRetries := s.config.MaxVerifyRetries

	for attempt := 0; attempt < maxRetries; attempt++ {
		passed, err := s.runVerifyAttempt(ctx, v, attempt, maxRetries, result)
		if err != nil {
			return err
		}
		if passed {
			return nil
		}
	}

	return s.finalVerifyPass(ctx, v, maxRetries)
}

// runVerifyAttempt runs one verify pass and, on failure, injects a repair
// prompt and runs a repair turn (which does NOT count toward MaxTurns).
// Returns (passed, error); a non-nil error is a real execution failure.
func (s *Session) runVerifyAttempt(ctx context.Context, v *verifier, attempt, maxRetries int, result *SessionResult) (bool, error) {
	res, err := v.run(ctx)
	if err != nil {
		return false, err // real execution failure
	}
	if res.Passed {
		s.emit(Event{Type: EventVerify, SessionID: s.id, Text: fmt.Sprintf("verify-after-edit: passed (%s)", res.Command)})
		return true, nil
	}

	// Verification failed — inject repair prompt with the actual command that failed.
	repairMsg := verifyRepairPrompt(res.Command, res.ExitCode, res.Output)
	s.emit(Event{Type: EventVerify, SessionID: s.id, Text: fmt.Sprintf("verify-after-edit: failed (attempt %d/%d), injecting repair prompt", attempt+1, maxRetries)})
	s.messages = append(s.messages, llm.UserMessage(repairMsg))

	return false, s.runRepairTurn(ctx, result)
}

// finalVerifyPass runs one last verification after the final repair attempt.
// Exhausted retries do not block the caller — the pass is reported via
// EventVerify either way.
func (s *Session) finalVerifyPass(ctx context.Context, v *verifier, maxRetries int) error {
	res, err := v.run(ctx)
	if err != nil {
		return err
	}
	if res.Passed {
		s.emit(Event{Type: EventVerify, SessionID: s.id, Text: fmt.Sprintf("verify-after-edit: passed after repairs (%s)", res.Command)})
	} else {
		s.emit(Event{Type: EventVerify, SessionID: s.id, Text: fmt.Sprintf("verify-after-edit: max retries (%d) exhausted, proceeding", maxRetries)})
	}
	return nil
}

// runRepairTurn executes one LLM repair turn outside the main MaxTurns budget.
// It calls the LLM, dispatches any tool calls, and appends messages. The repair
// turn's token usage is added to result.Usage so it's visible in session stats.
//
// Intentional simplification: repair turns skip compaction checks, turn counting,
// and turn-metric event emission that normal turns do. This is acceptable because
// repair turns are bounded by MaxVerifyRetries (default 2) and produce at most
// verifyOutputCap (16KB) of LLM-visible output — the cost impact is small and
// predictable. Adding full bookkeeping would require threading the turn counter
// and tracker into a shared path that would complicate the turn loop.
func (s *Session) runRepairTurn(ctx context.Context, result *SessionResult) error {
	resp, err := s.doLLMCall(ctx, -1) // turn=-1 marks it as a repair turn in events
	if err != nil {
		return err
	}

	// Accumulate usage (repair turns count toward total cost/token usage).
	result.Usage = result.Usage.Add(resp.Usage)
	if resp.Usage.EstimatedCost == 0 {
		result.Usage.EstimatedCost += llm.EstimateCost(s.pricingModel(resp), resp.Usage)
	}

	s.messages = append(s.messages, resp.Message)

	toolCalls := resp.ToolCalls()
	if len(toolCalls) == 0 {
		return nil // LLM responded with text only (e.g. "I fixed it")
	}

	_, _ = s.executeToolCalls(ctx, toolCalls, result, -1) // -1: repair turns run outside MaxTurns, so events carry no turn ordinal.
	return nil
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
