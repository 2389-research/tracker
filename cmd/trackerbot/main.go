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

	rm := tracker.NewRunManager(
		tracker.WithWorkDirBase(runsBase),
		tracker.WithMaxConcurrent(maxConcurrent),
	)
	runner := NewRunner(rm, RunnerDeps{
		NewThreadUI: bot.NewThreadUI,
		WorkDir:     workDir,
		NewID:       newGateID,
		ConfigBase:  tracker.Config{Backend: os.Getenv("TRACKERBOT_BACKEND")},
	})
	bot.SetRunner(runner)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	log.Printf("trackerbot: connecting via Socket Mode (max %d concurrent runs; runs under %s)…", maxConcurrent, runsBase)
	if err := bot.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("trackerbot: %v", err)
	}
	log.Println("trackerbot: shut down")
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
