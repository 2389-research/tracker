// ABOUTME: Agent session that runs the agentic loop: LLM call -> tool execution -> loop.
// ABOUTME: Manages conversation state, tool dispatch, event emission, and result collection.
package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/2389-research/mammoth-lite/agent/exec"
	"github.com/2389-research/mammoth-lite/agent/tools"
	"github.com/2389-research/mammoth-lite/llm"
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

// Session holds the state for a single agent conversation loop.
// A Session is single-use: Run must only be called once.
type Session struct {
	client   Completer
	config   SessionConfig
	handler  EventHandler
	registry *tools.Registry
	env      exec.ExecutionEnvironment
	messages []llm.Message
	id       string
	ran      bool
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

	for _, opt := range opts {
		opt(s)
	}

	// Register built-in tools if an environment is set.
	if s.env != nil {
		s.registry.Register(tools.NewReadTool(s.env))
		s.registry.Register(tools.NewWriteTool(s.env))
		s.registry.Register(tools.NewEditTool(s.env))
		s.registry.Register(tools.NewGlobTool(s.env))
		s.registry.Register(tools.NewBashTool(s.env, s.config.CommandTimeout, s.config.MaxCommandTimeout))
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

	result := SessionResult{
		SessionID: s.id,
		ToolCalls: make(map[string]int),
	}

	s.emit(Event{Type: EventSessionStart, SessionID: s.id})
	defer func() {
		result.Duration = time.Since(start)
		s.emit(Event{Type: EventSessionEnd, SessionID: s.id})
	}()

	// Initialize conversation.
	if s.config.SystemPrompt != "" {
		s.messages = append(s.messages, llm.SystemMessage(s.config.SystemPrompt))
	}
	s.messages = append(s.messages, llm.UserMessage(userInput))

	// Agentic loop.
	for turn := 1; turn <= s.config.MaxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			result.Error = err
			return result, err
		}

		s.emit(Event{Type: EventTurnStart, SessionID: s.id, Turn: turn})

		req := &llm.Request{
			Model:    s.config.Model,
			Provider: s.config.Provider,
			Messages: s.messages,
			Tools:    s.registry.Definitions(),
		}

		resp, err := s.client.Complete(ctx, req)
		if err != nil {
			result.Error = err
			s.emit(Event{Type: EventError, SessionID: s.id, Err: err})
			return result, err
		}

		result.Usage = result.Usage.Add(resp.Usage)
		result.Turns = turn

		s.messages = append(s.messages, resp.Message)

		toolCalls := resp.ToolCalls()
		if len(toolCalls) == 0 {
			text := resp.Text()
			if text != "" {
				s.emit(Event{Type: EventTextDelta, SessionID: s.id, Text: text})
			}
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

	return result, nil
}

// emit sends an event with the current timestamp to the session's event handler.
func (s *Session) emit(evt Event) {
	evt.Timestamp = time.Now()
	s.handler.HandleEvent(evt)
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
