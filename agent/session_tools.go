// ABOUTME: Session-scoped tool registration for tools that need a session-level
// ABOUTME: dependency (a runner, the event stream) rather than just the environment.
package agent

import "github.com/2389-research/tracker/agent/tools"

// registerStatusTool registers the report_status tool, wired to emit a
// first-class EventStatusUpdate on the session's event stream (#494).
func (s *Session) registerStatusTool() {
	statusTool := tools.NewReportStatusTool(func(status string) {
		s.emit(Event{Type: EventStatusUpdate, SessionID: s.id, Text: status})
	})
	if s.registry.Get(statusTool.Name()) == nil {
		s.registry.Register(statusTool)
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
