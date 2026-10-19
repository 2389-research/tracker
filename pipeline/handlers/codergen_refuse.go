// ABOUTME: Refuse-to-start: a node that cannot start for a host or config reason fails
// ABOUTME: routably and is not retried (no backend, backend gone on resume, ACP read_only, jail).
package handlers

import (
	"errors"
	"fmt"

	"github.com/2389-research/tracker/pipeline"
)

// Refuse-to-start kinds.
const (
	refuseWritablePaths      = "writable_paths"
	refuseNoBackend          = "no_backend"
	refuseBackendUnavailable = "backend_unavailable"
	refuseReadOnlyACP        = "read_only_acp"
)

// refuseLabels names each kind in the failure message.
var refuseLabels = map[string]string{
	refuseWritablePaths:      "writable_paths",
	refuseNoBackend:          "backend",
	refuseBackendUnavailable: "backend",
	refuseReadOnlyACP:        "tool_access read_only",
}

// refuseToStartError marks a node that must not start. Execute turns it into
// refuseToStartOutcome instead of a run-ending handler error.
type refuseToStartError struct {
	Kind string
	Err  error
}

func (e *refuseToStartError) Error() string { return e.Err.Error() }
func (e *refuseToStartError) Unwrap() error { return e.Err }

// refuseToStartOutcome builds the non-retryable, routable OutcomeFail for a
// refusal: retrying re-hits the same condition, but a `when ctx.outcome =
// fail` edge or fallback can still route it. No session ran, so there are no
// stats; the message rides last_response for the TUI and escalation prompts.
func (h *CodergenHandler) refuseToStartOutcome(kind string, node *pipeline.Node, cause error, prompt, artifactRoot string) (pipeline.Outcome, error) {
	msg := fmt.Sprintf("node %q: %s refuse-to-start (not retryable — a host capability/config condition): %v", node.ID, refuseLabels[kind], cause)
	outcome := pipeline.Outcome{
		Status:        pipeline.OutcomeFail,
		FailureReason: msg,
		ContextUpdates: map[string]string{
			pipeline.ContextKeyLastResponse:             msg,
			pipeline.ContextKeyResponsePrefix + node.ID: msg,
			pipeline.ContextKeyNodeCostExceeded:         "",
			pipeline.ContextKeyNodeNoProgress:           "",
		},
	}
	if err := pipeline.WriteStageArtifacts(artifactRoot, node.ID, prompt, msg, outcome); err != nil {
		return pipeline.Outcome{}, err
	}
	return outcome, nil
}

// selectBackendOrRefuse selects the node's backend. A refusal comes back as a
// routable outcome with a nil backend; any other selection error is returned
// wrapped with the node ID, as before.
func (h *CodergenHandler) selectBackendOrRefuse(node *pipeline.Node, prompt string, pctx *pipeline.PipelineContext) (pipeline.AgentBackend, pipeline.Outcome, error) {
	backend, err := h.selectBackendIn(node, pctx)
	if err == nil {
		return backend, pipeline.Outcome{}, nil
	}
	var refused *refuseToStartError
	if errors.As(err, &refused) {
		out, oerr := h.refuseToStartOutcome(refused.Kind, node, refused, prompt, h.resolveArtifactRoot(pctx))
		return nil, out, oerr
	}
	return nil, pipeline.Outcome{}, fmt.Errorf("node %q: %w", node.ID, err)
}

// refuseOnUnsupportedBackend refuses, before the agent starts, a node whose
// restriction the chosen backend cannot enforce: writable_paths on a
// non-native backend (#272), and tool_access: read_only on ACP (Tracker cannot
// restrict an out-of-process agent's tools).
func refuseOnUnsupportedBackend(node *pipeline.Node, backend pipeline.AgentBackend) (string, error) {
	if err := refuseWritablePathsOnUnsupportedBackend(node, backend); err != nil {
		return refuseWritablePaths, err
	}
	if _, acp := backend.(*ACPBackend); acp && isReadOnlyToolAccess(node.AgentConfig(nil).ToolAccess) {
		return refuseReadOnlyACP, fmt.Errorf("tool_access: read_only cannot be enforced on the acp backend (an out-of-process agent's tools can't be restricted); use native or claude-code")
	}
	return "", nil
}
