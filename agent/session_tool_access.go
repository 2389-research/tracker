// ABOUTME: restrictedRegistry: the tool registry a tool_access-restricted session keeps
// ABOUTME: (empty for none, exactly read/grep_search/glob for read_only).
package agent

import "github.com/2389-research/tracker/agent/tools"

// restrictedRegistry is the registry a restricted session keeps: empty for
// tool_access none (and any unrecognized value), and exactly the read_only
// set for read_only — every other tool, however it was registered
// (WithTools extras, spawn_agent, report_status), is dropped.
func restrictedRegistry(full *tools.Registry, cfg SessionConfig) *tools.Registry {
	r := tools.NewRegistry()
	r.SetOutputLimits(cfg.ToolOutputLimits)
	if cfg.ToolAccessMode() != ToolAccessReadOnly {
		return r
	}
	for _, name := range []string{"read", "grep_search", "glob"} {
		if t := full.Get(name); t != nil {
			r.Register(t)
		}
	}
	return r
}
