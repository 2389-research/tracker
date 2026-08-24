// ABOUTME: The agentic turn loop — extracted from session.go to keep it under the file-size gate.
// ABOUTME: Owns the per-turn durable snapshot lifecycle for sub-node resume (#427).
package agent

import (
	"context"
	"time"
)

// runTurnLoop executes the agentic loop and returns (stoppedNaturally, error).
//
// #427: on resume the loop starts at resumeTurn+1 (fresh runs start at 1). After
// every completed non-terminal turn it persists a durable turn snapshot; when the
// node reaches ANY terminal (natural stop, guard stop, or turn exhaustion — a
// nil-error return that the engine treats as node completion) it clears the
// snapshot so a later loop-restart of the node begins fresh. An error or
// ctx-cancel return deliberately LEAVES the snapshot on disk so `tracker -r`
// resumes mid-node. All of this is a no-op when TurnCheckpointPath is empty.
func (s *Session) runTurnLoop(ctx context.Context, start time.Time, tracker *ContextWindowTracker, result *SessionResult) (bool, error) {
	ts := &turnState{}
	for turn := s.resumeTurn + 1; turn <= s.config.MaxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			result.Error = err
			result.Duration = time.Since(start)
			return false, err
		}
		halt, stoppedNaturally, err := s.runOneTurn(ctx, turn, start, tracker, result, ts)
		if err != nil {
			return false, err
		}
		if halt {
			s.clearTurnSnapshot()
			return stoppedNaturally, nil
		}
		s.persistTurnSnapshot(turn)
	}
	s.clearTurnSnapshot()
	return false, nil
}
