// ABOUTME: Wires pctx-derived state onto the native SessionConfig (turn checkpointing).
// ABOUTME: Split out of codergen.go to keep that file under the size gate.
package handlers

import (
	"strings"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/pipeline"
)

// injectPriorEpisodes copies any prior-attempt episode summaries from pipeline
// context onto the native SessionConfig so a retry can avoid repeating known
// failing approaches. Returns the injected summaries (nil when none / non-native).
func (h *CodergenHandler) injectPriorEpisodes(runCfg pipeline.AgentRunConfig, pctx *pipeline.PipelineContext) []string {
	sc, ok := runCfg.Extra.(*agent.SessionConfig)
	if !ok || sc == nil {
		return nil
	}
	raw, ok := pctx.Get(pipeline.ContextKeyEpisodeSummaries)
	if !ok || strings.TrimSpace(raw) == "" {
		return nil
	}
	sc.PriorEpisodeSummaries = agent.ParseEpisodeSummaries(raw)
	return append([]string(nil), sc.PriorEpisodeSummaries...)
}

// applyTurnCheckpoint wires sub-node turn checkpointing (#427) onto the native
// session config when the node opts in via turn_checkpoint: true. It resolves a
// durable snapshot path under the secure run dir (#559) so the agent persists
// between-turns state and `tracker -r` resumes mid-node. Native-backend only
// (claude-code/acp don't drive the agent turn loop). It fails safe: if the run
// id is unavailable or run/node ids don't form a safe path, it leaves the path
// empty and the node keeps default node-boundary resume behavior.
func (h *CodergenHandler) applyTurnCheckpoint(node *pipeline.Node, pctx *pipeline.PipelineContext, sc *agent.SessionConfig) {
	if !node.AgentConfig(h.graphAttrs).TurnCheckpoint {
		return
	}
	runID := runIDFromContext(pctx)
	if runID == "" {
		return
	}
	path, err := pipeline.SecureTurnCheckpointPath(runID, node.ID)
	if err != nil {
		return // unsafe run/node id → degrade to node-boundary resume
	}
	sc.TurnCheckpointPath = path
}
