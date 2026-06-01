// ABOUTME: OverrideDetail describes a single validation-override event captured at edge selection.
// ABOUTME: Actor enum identifies who took the override edge; ErrValidationOverridden is the CLI exit sentinel.
package pipeline

import (
	"errors"
	"time"
)

// Actor identifies who took a validation-override edge. Stored on OverrideDetail.Actor.
// Defined as a named string type so JSON marshals as the bare string and the constant
// set is grep-able.
type Actor string

const (
	ActorHuman     Actor = "human"     // human-driven interviewer (TUI or non-TUI console)
	ActorAutopilot Actor = "autopilot" // any autopilot variant (LLM-backed or deterministic auto-approve)
	ActorWebhook   Actor = "webhook"   // external callback via WebhookInterviewer
	ActorUnknown   Actor = "unknown"   // third-party or future Interviewer with no recognized Actor() value
)

// OverrideDetail describes a single validation-override event: the gate that
// produced it, the label that selected the override edge, who acted, and the
// subgraph path (if propagated from a child run). Persisted on
// Checkpoint.ValidationOverrides and EngineResult.ValidationOverrides; emitted
// on PipelineEvent.Override when an override edge is traversed.
type OverrideDetail struct {
	// GateNodeID is the source node of the override edge (the wait.human gate).
	GateNodeID string `json:"gate_node_id"`

	// Label is the edge label of the override edge ("accept", "mark done", etc.).
	// Empty when the override edge has no label.
	Label string `json:"label,omitempty"`

	// Actor identifies who took the override edge.
	Actor Actor `json:"actor"`

	// SubgraphPath is populated when this override was propagated from a child
	// run via Outcome.ChildOverride. Outermost-to-innermost subgraph node IDs;
	// the leaf gate node ID lives in GateNodeID, not in SubgraphPath. Empty for
	// overrides taken in the run's own graph.
	SubgraphPath []string `json:"subgraph_path,omitempty"`

	// Timestamp is the moment the override edge was traversed. In the JSONL wire
	// format, the enclosing event line carries its own timestamp; this field is
	// primarily for Checkpoint persistence where there is no enclosing timestamp.
	Timestamp time.Time `json:"timestamp"`
}

// ErrValidationOverridden is the sentinel error returned by interpretRunResult
// when --fail-on-override is set and the run terminated as validation_overridden.
// The cobra entry checks errors.Is(err, ErrValidationOverridden) and exits with
// code 2 (distinct from generic-fail exit 1).
var ErrValidationOverridden = errors.New("run completed via validation_overridden")

// PrependSubgraphPath returns a copy of in with parentNodeID prepended to each
// entry's SubgraphPath. Used by subgraph and manager_loop handlers to lift
// child ValidationOverrides into parent-visible OverrideDetails with outermost-
// to-innermost ordering: at depth N each level prepends its own subgraph node ID,
// so by the time control returns to the outermost run the path enumerates the
// nesting chain top-down (leaf gate node ID stays on GateNodeID, never in
// SubgraphPath).
//
// The input is not mutated. Each output entry has a fresh SubgraphPath slice
// so callers may safely retain or mutate either side independently.
// Returns nil for nil/empty input so callers can rely on
// `if len(out) > 0` checks downstream.
func PrependSubgraphPath(in []OverrideDetail, parentNodeID string) []OverrideDetail {
	if len(in) == 0 {
		return nil
	}
	out := make([]OverrideDetail, len(in))
	for i, d := range in {
		newPath := make([]string, 0, len(d.SubgraphPath)+1)
		newPath = append(newPath, parentNodeID)
		newPath = append(newPath, d.SubgraphPath...)
		d.SubgraphPath = newPath
		out[i] = d
	}
	return out
}
