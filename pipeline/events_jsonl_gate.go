// ABOUTME: Maps the gate lifecycle payload onto the activity.jsonl entry (#509).
// ABOUTME: Kept separate so the gate schema can grow without inflating events_jsonl.go.
package pipeline

// applyGateFields copies the gate lifecycle payload into the log entry (#509).
func applyGateFields(entry *jsonlLogEntry, g *GateDetail) {
	entry.GateID = g.GateID
	entry.GateMode = g.Mode
	entry.GateLabel = g.Label
	entry.GatePrompt = g.Prompt
	entry.GateResponse = g.Response
	entry.GateOutcome = g.Outcome
	entry.GateActor = g.Actor
	entry.GateTimedOut = g.TimedOut
	if len(g.Choices) > 0 {
		// Copy to defend against later mutation of the source slice.
		entry.GateChoices = append([]string(nil), g.Choices...)
	}
	if len(g.Question) > 0 {
		entry.GateQuestions = append([]GateQuestion(nil), g.Question...)
	}
	if g.Error != "" && entry.Error == "" {
		entry.Error = g.Error
	}
}
