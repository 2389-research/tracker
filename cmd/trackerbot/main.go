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
	"strings"

	tracker "github.com/2389-research/tracker"
	"github.com/2389-research/tracker/pipeline"
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

	configureAllowlist(bot)

	// Fail-closed per-run budget so chat-triggered runs never spend unbounded.
	// Default $5; TRACKERBOT_MAX_COST_CENTS=0 disables (operator's explicit choice).
	maxCostCents := envInt("TRACKERBOT_MAX_COST_CENTS", 500)

	// The runner pins a deterministic workdir + checkpoint per thread, so the
	// RunManager only provides concurrency/lifecycle here.
	rm := tracker.NewRunManager(tracker.WithMaxConcurrent(maxConcurrent))
	configBase := tracker.Config{
		Backend: os.Getenv("TRACKERBOT_BACKEND"),
		Budget:  pipeline.BudgetLimits{MaxCostCents: maxCostCents},
	}
	st := openStore(filepath.Join(runsBase, "trackerbot-state.json"))
	runner := NewRunner(rm, RunnerDeps{
		NewThreadUI:  bot.NewThreadUI,
		WorkDir:      workDir,
		RunsBase:     runsBase,
		NewID:        newGateID,
		ConfigBase:   configBase,
		Intent:       buildIntentResolver(configBase),
		Store:        st,
		KeepWorkdirs: os.Getenv("TRACKERBOT_KEEP_WORKDIRS") == "1",
	})
	bot.SetRunner(runner)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	resumeOrphans(ctx, runner, st.list())

	log.Printf("trackerbot: connecting via Socket Mode (max %d concurrent runs; runs under %s)…", maxConcurrent, runsBase)
	if err := bot.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("trackerbot: %v", err)
	}
	log.Println("trackerbot: shut down")
}

// configureAllowlist restricts who may drive the bot from TRACKERBOT_ALLOWED_USERS,
// warning loudly when unset (open to everyone in the bot's channels).
func configureAllowlist(bot *SlackBot) {
	allowed := splitCSV(os.Getenv("TRACKERBOT_ALLOWED_USERS"))
	if len(allowed) == 0 {
		log.Printf("trackerbot: WARNING — no TRACKERBOT_ALLOWED_USERS set; anyone in the bot's channels can start paid runs")
		return
	}
	bot.SetAllowlist(allowed)
	log.Printf("trackerbot: restricted to %d allowlisted user(s)", len(allowed))
}

// resumeOrphans sweeps workdirs no store record references, then re-launches the
// runs that were active when a previous process exited.
func resumeOrphans(ctx context.Context, runner *Runner, orphans []RunRecord) {
	runner.SweepOrphans(orphans)
	if len(orphans) == 0 {
		return
	}
	log.Printf("trackerbot: resuming %d interrupted run(s) from a previous session", len(orphans))
	for _, rec := range orphans {
		go runner.Resume(ctx, rec)
	}
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

// splitCSV parses a comma-separated env value into trimmed, non-empty items.
func splitCSV(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
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
