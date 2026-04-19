// ABOUTME: Turn-budget checkpoint evaluation for the agent session loop.
// ABOUTME: Returns the message to inject (if any) when a turn threshold is exactly hit.
package agent

import "math"

// evalCheckpoint returns the checkpoint message to inject at the given turn,
// or "" if no checkpoint fires. Each checkpoint fires exactly once: on the
// turn number that equals ceil(fraction * maxTurns). Callers do not need to
// track fired state — the turn-number match is deterministic.
func evalCheckpoint(checkpoints []Checkpoint, turn, maxTurns int) string {
	for _, cp := range checkpoints {
		// Compute the exact turn this checkpoint fires on.
		// Ceiling: fraction=0.51 with maxTurns=10 fires on turn 6 (not 5).
		triggerTurn := int(math.Ceil(cp.Fraction * float64(maxTurns)))
		if triggerTurn < 1 {
			triggerTurn = 1
		}
		if turn == triggerTurn {
			return cp.Message
		}
	}
	return ""
}
