// ABOUTME: Agent session that runs the agentic loop: LLM call -> tool execution -> loop.
// ABOUTME: Manages conversation state, tool dispatch, event emission, and result collection.
package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
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
	client        Completer
	config        SessionConfig
	handler       EventHandler
	registry      *tools.Registry
	env           exec.ExecutionEnvironment
	sessionRunner tools.SessionRunner
	steering      <-chan string
	messages      []llm.Message
	id            string
	ran           bool
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
		client:   client,
		config:   config,
		handler:  NoopHandler,
		registry: tools.NewRegistry(),
		id:       generateSessionID(),
	}

	// Apply all options first (including WithEnvironment and WithTools).
	for _, opt := range opts {
		opt(s)
	}
	s.registry.SetOutputLimits(s.config.ToolOutputLimits)

	// Register built-in tools if an environment is set.
	// Custom tools registered via WithTools take precedence over built-ins.
	if s.env != nil {
		builtins := builtInToolsForConfig(s.config, s.env)
		for _, t := range builtins {
			// Only register built-in if no custom tool with the same name exists.
			if s.registry.Get(t.Name()) == nil {
				s.registry.Register(t)
			}
		}
	}

	// Register spawn_agent tool if a session runner is provided.
	if s.sessionRunner != nil {
		spawnTool := tools.NewSpawnAgentTool(s.sessionRunner)
		if s.registry.Get(spawnTool.Name()) == nil {
			s.registry.Register(spawnTool)
		}
	}

	return s, nil
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
		s.emit(Event{Type: EventSessionEnd, SessionID: s.id})
	}()

	// Initialize conversation.
	if s.config.SystemPrompt != "" {
		s.messages = append(s.messages, llm.SystemMessage(s.config.SystemPrompt))
	}
	s.messages = append(s.messages, llm.UserMessage(userInput))

	// Agentic loop.
	stoppedNaturally := false
	var lastToolSignature string
	consecutiveLoopCount := 0
	for turn := 1; turn <= s.config.MaxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			result.Error = err
			result.Duration = time.Since(start)
			return result, err
		}

		if s.steering != nil {
			for {
				select {
				case msg := <-s.steering:
					s.messages = append(s.messages, llm.UserMessage("[STEERING] "+msg))
					s.emit(Event{Type: EventSteeringInjected, SessionID: s.id, Text: msg})
				default:
					goto steeringDrained
				}
			}
		}
	steeringDrained:

		s.emit(Event{Type: EventTurnStart, SessionID: s.id, Turn: turn})

		req := &llm.Request{
			Model:    s.config.Model,
			Provider: s.config.Provider,
			Messages: s.messages,
			Tools:    s.registry.Definitions(),
			TraceObservers: []llm.TraceObserver{
				llm.TraceObserverFunc(func(traceEvt llm.TraceEvent) {
					s.emitLLMTraceEvent(turn, traceEvt)
				}),
			},
		}

		resp, err := s.client.Complete(ctx, req)
		if err != nil {
			result.Error = err
			result.Duration = time.Since(start)
			s.emit(Event{Type: EventError, SessionID: s.id, Err: err})
			return result, err
		}

		result.Usage = result.Usage.Add(resp.Usage)
		result.Turns = turn

		tracker.Update(resp.Usage)
		if tracker.ShouldWarn() {
			s.emit(Event{
				Type:               EventContextWindowWarning,
				SessionID:          s.id,
				Turn:               turn,
				ContextUtilization: tracker.Utilization(),
			})
			tracker.MarkWarned()
		}

		s.messages = append(s.messages, resp.Message)

		toolCalls := resp.ToolCalls()
		if len(toolCalls) == 0 {
			text := resp.Text()
			if text != "" {
				s.emit(Event{Type: EventTextDelta, SessionID: s.id, Text: text})
			}
			s.emit(Event{Type: EventTurnEnd, SessionID: s.id, Turn: turn})
			stoppedNaturally = true
			break
		}

		// Compute tool call signature for loop detection.
		// Include tool arguments so that different bash commands don't
		// count as the same repeated call.
		toolSigs := make([]string, len(toolCalls))
		for i, call := range toolCalls {
			toolSigs[i] = call.Name + ":" + string(call.Arguments)
		}
		signature := strings.Join(toolSigs, ",")

		if signature == lastToolSignature {
			consecutiveLoopCount++
		} else {
			lastToolSignature = signature
			consecutiveLoopCount = 1
		}

		if consecutiveLoopCount >= s.config.LoopDetectionThreshold {
			loopErr := fmt.Errorf("loop detected: same tool calls repeated %d times", consecutiveLoopCount)
			s.emit(Event{Type: EventError, SessionID: s.id, Err: loopErr})
			result.LoopDetected = true
			s.emit(Event{Type: EventTurnEnd, SessionID: s.id, Turn: turn})
			break
		}

		// Execute each tool call and collect results.
		var toolResults []llm.ContentPart
		for _, call := range toolCalls {
			s.emit(Event{
				Type:      EventToolCallStart,
				SessionID: s.id,
				ToolName:  call.Name,
				ToolInput: string(call.Arguments),
			})

			toolResult := s.registry.Execute(ctx, call)
			result.ToolCalls[call.Name]++

			s.emit(Event{
				Type:       EventToolCallEnd,
				SessionID:  s.id,
				ToolName:   call.Name,
				ToolOutput: toolResult.Content,
				ToolError:  boolToErrStr(toolResult.IsError),
			})

			toolResults = append(toolResults, llm.ContentPart{
				Kind:       llm.KindToolResult,
				ToolResult: &toolResult,
			})
		}

		s.messages = append(s.messages, llm.Message{
			Role:    llm.RoleTool,
			Content: toolResults,
		})

		s.emit(Event{Type: EventTurnEnd, SessionID: s.id, Turn: turn})
	}

	if !stoppedNaturally {
		result.MaxTurnsUsed = true
	}

	result.ContextUtilization = tracker.Utilization()
	result.Duration = time.Since(start)
	return result, nil
}

// emit sends an event with the current timestamp to the session's event handler.
func (s *Session) emit(evt Event) {
	evt.Timestamp = time.Now()
	s.handler.HandleEvent(evt)
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
