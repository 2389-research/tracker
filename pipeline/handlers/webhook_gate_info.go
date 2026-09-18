// ABOUTME: GateAware side of WebhookInterviewer (#631) — receives the gate's identity and
// ABOUTME: structured options from the handler and attaches them to the next outbound payload.
package handlers

// BeginGate implements GateAware: the handler hands over the gate's identity
// and structured options immediately before the matching Ask* call, and the
// next outbound payload carries them (node_id, default, options).
func (w *WebhookInterviewer) BeginGate(info GateInfo) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.gate = info
}

// takeGate returns and clears the pending GateInfo. BeginGate and the Ask*
// that follows are two calls, and one interviewer can serve concurrent gates
// (parallel branches), so the info is attached only when its option labels
// are exactly the choices this ask presents; otherwise it is dropped and the
// payload degrades to the flat choices — never another gate's buttons.
func (w *WebhookInterviewer) takeGate(choices []WebhookGateChoice) GateInfo {
	w.mu.Lock()
	defer w.mu.Unlock()
	g := w.gate
	w.gate = GateInfo{}
	if !gateInfoMatchesChoices(g, choices) {
		return GateInfo{}
	}
	return g
}

// gateInfoMatchesChoices reports whether info's options are, label for label
// and in order, the choices being asked (both empty for an unlabeled gate).
func gateInfoMatchesChoices(info GateInfo, choices []WebhookGateChoice) bool {
	if len(info.Options) != len(choices) {
		return false
	}
	for i, c := range choices {
		if info.Options[i].Label != c.Label {
			return false
		}
	}
	return true
}
