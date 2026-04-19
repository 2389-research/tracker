// ABOUTME: Turn-budget checkpoint evaluation for the agent session loop.
// ABOUTME: Returns the message to inject (if any) when a turn threshold is exactly hit.
package agent

// evalCheckpoint returns the checkpoint message to inject at the given turn,
// or "" if no checkpoint fires. Each checkpoint fires exactly once: on the
// turn number that equals int(fraction * maxTurns). Callers do not need to
// track fired state — the turn-number match is deterministic.
func evalCheckpoint(checkpoints []Checkpoint, turn, maxTurns int) string {
	for _, cp := range checkpoints {
		// Compute the exact turn this checkpoint fires on.
		// Truncation: fraction=0.5 with maxTurns=80 fires on turn 40.
		triggerTurn := int(cp.Fraction * float64(maxTurns))
		if triggerTurn < 1 {
			triggerTurn = 1
		}
		if turn == triggerTurn {
			return cp.Message
		}
	}
	return ""
}
