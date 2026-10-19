// ABOUTME: restrictedRegistry: the tool registry a tool_access-restricted session keeps
// ABOUTME: (empty for none, exactly read/grep_search/glob for read_only).
package agent

import (
	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/agent/tools"
)

// restrictedRegistry is the registry a restricted session keeps: empty for
// tool_access none (and any unrecognized value), and for read_only exactly
// the guarded built-ins, freshly built against env. Nothing is carried over
// from the session's registry — not WithTools extras, spawn_agent or
// report_status, and not a caller's tool that merely shares a built-in's
// name. With no environment there is nothing to guard, so no tools.
func restrictedRegistry(cfg SessionConfig, env exec.ExecutionEnvironment) *tools.Registry {
	r := tools.NewRegistry()
	r.SetOutputLimits(cfg.ToolOutputLimits)
	if cfg.ToolAccessMode() != ToolAccessReadOnly || env == nil {
		return r
	}
	for _, t := range tools.NewReadOnlyTools(env) {
		r.Register(t)
	}
	return r
}
