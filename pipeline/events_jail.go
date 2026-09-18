// ABOUTME: EventJailDegraded and its payload (#648) — the recorded degradation of a
// ABOUTME: writable_paths_mode: prefer node to an unjailed run on a host without Landlock.
package pipeline

// EventJailDegraded fires when an agent node declared writable_paths with
// `writable_paths_mode: prefer` and the host cannot enforce the jail
// (Landlock ABI < 3 / non-Linux — the G3 host-capability gate), so the
// node ran UNJAILED (#648). Exactly one per node execution attempt,
// emitted by the codergen handler BEFORE the session's first turn. It is
// a loud, recorded degradation, not an error: the node's Bash subprocess
// has its pre-#272 write reach; in-process Write/Edit/ApplyPatch stay
// policy-bounded to the declared globs. Authoring (bad globs) and backend
// (claude-code/acp) refusals never degrade — they still refuse. Carries
// JailDegradedDetail; surfaced by the TUI as a warning line, by `tracker
// diagnose` as a suggestion, and by run.json as jail_degraded_nodes.
const EventJailDegraded PipelineEventType = "jail_degraded"

// JailDegradedDetail is the payload for EventJailDegraded (#648). Mode is the
// node's writable_paths_mode (always "prefer" — require never degrades);
// Reason is the host-capability probe error that would have refused the node
// under require; DeclaredGlobs are the writable_paths the author declared and
// that the Bash subprocess is NOT bounded by on this run.
type JailDegradedDetail struct {
	Mode          string   `json:"mode"`
	Reason        string   `json:"reason"`
	DeclaredGlobs []string `json:"declared_globs,omitempty"`
}
