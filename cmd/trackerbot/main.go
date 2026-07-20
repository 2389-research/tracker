// ABOUTME: Entry point for trackerbot — a Slack front-end that drives Tracker pipelines.
// ABOUTME: Wires the Socket Mode transport, RunManager, and Runner together.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"

	tracker "github.com/2389-research/tracker"
)

func main() {
	botToken := os.Getenv("SLACK_BOT_TOKEN")
	appToken := os.Getenv("SLACK_APP_TOKEN")
	if botToken == "" || appToken == "" {
		fmt.Fprintln(os.Stderr, "trackerbot: set SLACK_BOT_TOKEN (xoxb-…) and SLACK_APP_TOKEN (xapp-…)")
		os.Exit(1)
	}

	workDir := envOr("TRACKERBOT_WORKDIR", ".")
	runsBase := envOr("TRACKERBOT_RUNS", filepath.Join(os.TempDir(), "trackerbot-runs"))
	maxConcurrent := envInt("TRACKERBOT_MAX_CONCURRENT", 8)

	bot, err := NewSlackBot(botToken, appToken)
	if err != nil {
		log.Fatalf("trackerbot: %v", err)
	}

	// The runner pins a deterministic workdir + checkpoint per thread, so the
	// RunManager only provides concurrency/lifecycle here.
	rm := tracker.NewRunManager(tracker.WithMaxConcurrent(maxConcurrent))
	configBase := tracker.Config{Backend: os.Getenv("TRACKERBOT_BACKEND")}
	st := openStore(filepath.Join(runsBase, "trackerbot-state.json"))
	runner := NewRunner(rm, RunnerDeps{
		NewThreadUI: bot.NewThreadUI,
		WorkDir:     workDir,
		RunsBase:    runsBase,
		NewID:       newGateID,
		ConfigBase:  configBase,
		Intent:      buildIntentResolver(configBase),
		Store:       st,
	})
	bot.SetRunner(runner)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Resume any runs that were active when a previous process exited.
	if orphans := st.list(); len(orphans) > 0 {
		log.Printf("trackerbot: resuming %d interrupted run(s) from a previous session", len(orphans))
		for _, rec := range orphans {
			go runner.Resume(ctx, rec)
		}
	}

	log.Printf("trackerbot: connecting via Socket Mode (max %d concurrent runs; runs under %s)…", maxConcurrent, runsBase)
	if err := bot.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("trackerbot: %v", err)
	}
	log.Println("trackerbot: shut down")
}

// buildIntentResolver enables natural-language routing when an LLM is available,
// falling back to the deterministic "run <workflow>" grammar otherwise.
func buildIntentResolver(cfg tracker.Config) IntentResolver {
	client, err := tracker.NewLLMClient(cfg)
	if err != nil {
		log.Printf("trackerbot: LLM intent unavailable (%v) — using the 'run <workflow>' grammar", err)
		return grammarResolver{}
	}
	model := envOr("TRACKERBOT_MODEL", "claude-haiku-4-5-20251001")
	log.Printf("trackerbot: natural-language intent enabled (model %s)", model)
	return newLLMIntentResolver(client, model)
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

// newGateID returns a short random id used to correlate a gate with its answer.
func newGateID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
