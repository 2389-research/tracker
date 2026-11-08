// ABOUTME: Tests the native session's explicit unlimited model-turn policy.
// ABOUTME: Covers natural completion, cancellation, durable resume, and budget checkpoints.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/2389-research/tracker/llm"
)

func TestConfigMaxTurnsPolicy(t *testing.T) {
	for _, turns := range []int{-3, -2, -1, 0, 1, 80} {
		t.Run(fmt.Sprint(turns), func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.MaxTurns = turns
			wantValid := turns == -1 || turns > 0
			if err := cfg.Validate(); (err == nil) != wantValid {
				t.Fatalf("MaxTurns=%d validation = %v, want valid=%v", turns, err, wantValid)
			}
		})
	}
}

func unlimitedToolResponses(count int, name string) []*llm.Response {
	responses := make([]*llm.Response, count)
	for i := range responses {
		responses[i] = toolCallResponse(fmt.Sprint(i), name)
		// Distinct calls represent progress without weakening loop detection.
		responses[i].Message.Content[0].ToolCall.Arguments = json.RawMessage(fmt.Sprintf(`{"step":%d}`, i))
	}
	return responses
}

func TestSessionUnlimitedNaturalCompletion(t *testing.T) {
	client := &mockCompleter{responses: unlimitedToolResponses(82, "work")}
	cfg := DefaultConfig()
	cfg.MaxTurns = -1
	cfg.Checkpoints = []Checkpoint{{Fraction: 0.5, Message: "turn budget halfway"}}
	var budgetMessages int
	client.onComplete = func(req *llm.Request) {
		for _, msg := range req.Messages {
			if msg.Text() == "turn budget halfway" {
				budgetMessages++
			}
		}
	}
	sess := mustNewSession(t, client, cfg, WithTools(&stubTool{name: "work", output: "ok"}))
	result, err := sess.Run(context.Background(), "work until complete")
	if err != nil {
		t.Fatal(err)
	}
	if result.Turns != 83 || result.TotalToolCalls() != 82 || result.MaxTurnsUsed || result.LoopDetected {
		t.Fatalf("unexpected result: %+v", result)
	}
	if budgetMessages != 0 {
		t.Fatalf("injected fractional budget checkpoint without a budget (%d sightings)", budgetMessages)
	}
}

func TestSessionUnlimitedCancellationAndResume(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := DefaultConfig()
	cfg.MaxTurns = -1
	cfg.WorkingDir = t.TempDir()
	cfg.TurnCheckpointPath = filepath.Join(t.TempDir(), "turn.json")
	tool := &cancelOnNthTool{name: "work", n: 81, cancel: cancel}
	client := &mockCompleter{responses: unlimitedToolResponses(82, "work")}
	sess := mustNewSession(t, client, cfg, WithTools(tool))
	result, err := sess.Run(ctx, "work until complete")
	if !errors.Is(err, context.Canceled) || result.MaxTurnsUsed || result.Turns != 81 {
		t.Fatalf("canceled result: turns=%d exhausted=%v err=%v", result.Turns, result.MaxTurnsUsed, err)
	}
	snap, err := LoadTurnSnapshot(cfg.TurnCheckpointPath)
	if err != nil || snap == nil || snap.Turn != 81 {
		t.Fatalf("cancellation snapshot = %+v, err=%v", snap, err)
	}
	resumed := mustNewSession(t, &mockCompleter{}, cfg)
	result, err = resumed.Run(context.Background(), "resume")
	if err != nil || result.Turns != 82 || result.MaxTurnsUsed || result.TotalToolCalls() != 81 {
		t.Fatalf("resumed result = %+v, err=%v", result, err)
	}
	if _, err := os.Stat(cfg.TurnCheckpointPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("terminal snapshot cleanup: %v", err)
	}
}

func TestEvalCheckpoints_Unlimited(t *testing.T) {
	cps := []Checkpoint{{Fraction: 0.5, Message: "halfway"}}
	if got := evalCheckpoint(cps, 1, -1); got != "" {
		t.Fatalf("unlimited checkpoint = %q, want none", got)
	}
}

func TestSessionFiniteAndDefaultTurnLimits(t *testing.T) {
	for _, limit := range []int{3, DefaultConfig().MaxTurns} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.MaxTurns = limit
			client := &mockCompleter{responses: unlimitedToolResponses(82, "work")}
			sess := mustNewSession(t, client, cfg, WithTools(&stubTool{name: "work", output: "ok"}))
			result, err := sess.Run(context.Background(), "work")
			if err != nil || result.Turns != limit || !result.MaxTurnsUsed || result.LoopDetected {
				t.Fatalf("finite result=%+v, err=%v", result, err)
			}
		})
	}
}

func TestSessionUnlimitedRetainsGuards(t *testing.T) {
	for _, guard := range []string{"cost", "progress", "loop"} {
		t.Run(guard, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.MaxTurns = -1
			responses := []*llm.Response{lengthTruncatedResponse(0.006), lengthTruncatedResponse(0.006)}
			switch guard {
			case "cost":
				cfg.MaxCostUSD = 0.01
			case "progress":
				cfg.NoProgressTurns = 2
			case "loop":
				responses = nil
				for range cfg.LoopDetectionThreshold {
					responses = append(responses, toolCallResponse("repeat", "work"))
				}
			}
			sess := mustNewSession(t, &mockCompleter{responses: responses}, cfg, WithTools(&stubTool{name: "work", output: "ok"}))
			result, err := sess.Run(context.Background(), "work")
			if err != nil {
				t.Fatal(err)
			}
			switch guard {
			case "cost":
				if !result.NodeCostExceeded || result.MaxTurnsUsed {
					t.Fatalf("cost guard result=%+v", result)
				}
			case "progress":
				if !result.NoProgressDetected || result.MaxTurnsUsed {
					t.Fatalf("progress guard result=%+v", result)
				}
			case "loop":
				if !result.LoopDetected || result.Turns != cfg.LoopDetectionThreshold {
					t.Fatalf("loop guard result=%+v", result)
				}
			}
		})
	}
}
