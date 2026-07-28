// ABOUTME: Gate lifecycle event emission for the human gate handler (#509).
// ABOUTME: Emits gate_opened/gate_resolved pairs correlated by GateDetail.GateID.
package handlers

import (
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/2389-research/tracker/pipeline"
)

// HumanOption configures a HumanHandler.
type HumanOption func(*HumanHandler)

// WithHumanPipelineEmitter makes the handler emit gate lifecycle events
// (EventGateOpened / EventGateResolved) around every interviewer call (#509).
// Without it the handler emits nothing, which is what every non-engine caller
// (tests, embedders constructing the handler directly) gets by default.
func WithHumanPipelineEmitter(e pipeline.PipelineEventHandler) HumanOption {
	return func(h *HumanHandler) { h.emitter = e }
}

// emitGateOpened emits EventGateOpened and returns the GateDetail whose GateID
// correlates the matching resolution (#509). Returns nil when no emitter is
// configured, which makes every emit* call below a no-op.
func (h *HumanHandler) emitGateOpened(node *pipeline.Node, pctx *pipeline.PipelineContext, prompt string) *pipeline.GateDetail {
	if h.emitter == nil {
		return nil
	}
	mode := node.HumanConfig().Mode
	if mode == "" {
		mode = "choice"
	}
	gate := &pipeline.GateDetail{
		GateID:   newGateID(),
		Mode:     mode,
		Label:    node.Label,
		Prompt:   truncateGatePrompt(prompt),
		Choices:  h.gateChoices(node, mode),
		Question: interviewGateQuestions(node, pctx, mode),
	}
	h.emit(node, pctx, pipeline.EventGateOpened, gate, nil)
	return gate
}

// interviewGateQuestions returns the questions an interview-mode gate actually
// presents. In interview mode the responder never sees the node prompt — the
// handler passes parsed []Question to AskInterview — so without this the opened
// event would describe a question that was not asked, and a consumer could not
// map an answer in the resolution summary back to its question. Parsed from the
// same context keys executeInterview reads, so the two cannot disagree.
// Nil for every other mode.
func interviewGateQuestions(node *pipeline.Node, pctx *pipeline.PipelineContext, mode string) []pipeline.GateQuestion {
	if mode != "interview" || pctx == nil {
		return nil
	}
	questionsKey, _ := resolveInterviewKeys(node)
	parsed := parseInterviewQuestions(resolveAgentOutput(pctx, questionsKey))
	if len(parsed) == 0 {
		// Zero questions falls back to freeform against the node prompt, which
		// the Prompt field already carries.
		return nil
	}
	out := make([]pipeline.GateQuestion, 0, len(parsed))
	for _, q := range parsed {
		out = append(out, pipeline.GateQuestion{
			ID:      fmt.Sprintf("q%d", q.Index),
			Text:    q.Text,
			Options: q.Options,
			IsYesNo: q.IsYesNo,
		})
	}
	return out
}

// emit stamps the run ID and forwards a gate event. Handler-originated events
// bypass Engine.emit (which stamps RunID itself), so without this the events
// would be unattributable when one event handler serves concurrent runs.
func (h *HumanHandler) emit(node *pipeline.Node, pctx *pipeline.PipelineContext, t pipeline.PipelineEventType, gate *pipeline.GateDetail, err error) {
	var runID string
	if pctx != nil {
		runID, _ = pctx.GetInternal(pipeline.InternalKeyRunID)
	}
	h.emitter.HandlePipelineEvent(pipeline.PipelineEvent{
		Type:      t,
		RunID:     runID,
		NodeID:    node.ID,
		Timestamp: time.Now(),
		Gate:      gate,
		Err:       err,
	})
}

// emitGateResolved emits EventGateResolved carrying the same GateID as the
// matching gate_opened. Called on every exit path — success, fail, timeout, and
// interviewer error — so a stream consumer never sees a gate stay open.
func (h *HumanHandler) emitGateResolved(node *pipeline.Node, pctx *pipeline.PipelineContext, gate *pipeline.GateDetail, outcome pipeline.Outcome, actor pipeline.Actor, timedOut bool, err error) {
	if h.emitter == nil || gate == nil {
		return
	}
	resolved := &pipeline.GateDetail{
		GateID:   gate.GateID,
		Mode:     gate.Mode,
		Label:    gate.Label,
		Response: gateResponseOf(node, outcome),
		Outcome:  string(outcome.Status),
		Actor:    actor,
		TimedOut: timedOut,
	}
	if err != nil {
		resolved.Error = err.Error()
	}
	h.emit(node, pctx, pipeline.EventGateResolved, resolved, err)
}

// gateChoices returns the options presented to the responder for this mode:
// the fixed pair for yes_no, otherwise the outgoing edge labels (empty for an
// unlabeled freeform gate).
func (h *HumanHandler) gateChoices(node *pipeline.Node, mode string) []string {
	if mode == "yes_no" {
		return []string{"Yes", "No"}
	}
	return collectEdgeLabels(h.graph, node.ID)
}

// gateResponseOf extracts what the responder actually returned from the gate's
// outcome: the per-node human response when the mode recorded one (freeform,
// interview, timeout-default), else the routing label selected in choice and
// yes_no modes.
func gateResponseOf(node *pipeline.Node, outcome pipeline.Outcome) string {
	if v, ok := outcome.ContextUpdates[pipeline.ContextKeyResponsePrefix+node.ID]; ok && v != "" {
		return v
	}
	return outcome.PreferredLabel
}

// truncateGatePrompt caps the prompt carried on GateDetail at
// pipeline.GateMaxPromptBytes. See GateMaxPromptBytes for why.
func truncateGatePrompt(prompt string) string {
	if len(prompt) <= pipeline.GateMaxPromptBytes {
		return prompt
	}
	// Back off to a rune boundary so the payload stays valid UTF-8 (at most
	// three iterations).
	cut := prompt[:pipeline.GateMaxPromptBytes]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}
