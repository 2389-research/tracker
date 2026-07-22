// ABOUTME: Entry point for trackerchat — a terminal REPL front-end for Tracker.
// ABOUTME: A second transport-boundary consumer alongside trackerbot, sharing the
// ABOUTME: chatops Runner; only the terminal I/O differs from Slack.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"

	tracker "github.com/2389-research/tracker"
	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/transport/chatops"
	"github.com/2389-research/tracker/transport/cli"
)

func main() {
	log.SetFlags(0)
	workDir := envOr("TRACKERCHAT_WORKDIR", ".")
	runsBase := envOr("TRACKERCHAT_RUNS", filepath.Join(os.TempDir(), "trackerchat-runs"))
	maxCostCents := envInt("TRACKERCHAT_MAX_COST_CENTS", 500) // fail-closed per-run budget; 0 disables

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	session := cli.NewSession(os.Stdout)
	rm := tracker.NewRunManager()
	configBase := tracker.Config{
		Backend: os.Getenv("TRACKERCHAT_BACKEND"),
		Budget:  pipeline.BudgetLimits{MaxCostCents: maxCostCents},
	}
	runner := chatops.NewRunner(rm, chatops.RunnerDeps{
		NewThreadUI:  session.ThreadUI,
		WorkDir:      workDir,
		RunsBase:     runsBase,
		NewID:        newGateID,
		ConfigBase:   configBase,
		Intent:       buildIntentResolver(configBase),
		KeepWorkdirs: os.Getenv("TRACKERCHAT_KEEP_WORKDIRS") == "1",
	})

	if err := session.Run(ctx, os.Stdin, runner); err != nil {
		log.Fatalf("trackerchat: %v", err)
	}
}

// buildIntentResolver enables natural-language routing when an LLM is available,
// falling back to the deterministic "run <workflow>" grammar otherwise — the
// same policy trackerbot uses.
func buildIntentResolver(cfg tracker.Config) chatops.IntentResolver {
	client, err := tracker.NewLLMClient(cfg)
	if err != nil {
		log.Printf("trackerchat: LLM intent unavailable (%v) — using the 'run <workflow>' grammar", err)
		return chatops.GrammarResolver{}
	}
	model := envOr("TRACKERCHAT_MODEL", "claude-haiku-4-5")
	return chatops.NewLLMIntentResolver(client, model)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// newGateID returns a short random id correlating a gate with its answer.
func newGateID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
