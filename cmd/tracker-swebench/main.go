// ABOUTME: CLI entry point for the tracker-swebench SWE-bench benchmarking harness.
// ABOUTME: Runs tracker's code agent against SWE-bench Lite instances and records predictions.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	dataset := flag.String("dataset", "", "path to JSONL file (required)")
	model := flag.String("model", "claude-sonnet-4-6", "model name")
	provider := flag.String("provider", "anthropic", "provider name")
	gatewayURL := flag.String("gateway-url", "", "Cloudflare AI Gateway URL")
	output := flag.String("output", "./predictions.jsonl", "output file for predictions")
	resultsDir := flag.String("results-dir", "./results", "results directory")
	maxTurns := flag.Int("max-turns", 50, "maximum agent turns per instance")
	timeout := flag.Duration("timeout", 10*time.Minute, "per-instance timeout")
	instance := flag.String("instance", "", "single instance filter (optional)")
	force := flag.Bool("force", false, "re-run completed instances")
	dockerImage := flag.String("docker-image", "tracker-swebench-base", "Docker image to use")

	flag.Parse()

	if *dataset == "" {
		log.Fatal("--dataset is required")
	}

	instances, err := LoadDataset(*dataset)
	if err != nil {
		log.Fatalf("load dataset: %v", err)
	}

	// Filter to single instance if requested.
	if *instance != "" {
		found := false
		for _, inst := range instances {
			if inst.InstanceID == *instance {
				instances = []Instance{inst}
				found = true
				break
			}
		}
		if !found {
			log.Fatalf("instance %q not found in dataset", *instance)
		}
	}

	// Create results directories.
	logsDir := filepath.Join(*resultsDir, "logs")
	cacheDir := filepath.Join(*resultsDir, "repo-cache")
	for _, dir := range []string{*resultsDir, logsDir, cacheDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("create dir %q: %v", dir, err)
		}
	}

	// Write run metadata.
	meta := RunMeta{
		Model:      *model,
		Provider:   *provider,
		GatewayURL: *gatewayURL,
		Dataset:    *dataset,
		MaxTurns:   *maxTurns,
		Timeout:    timeout.String(),
	}
	metaPath := filepath.Join(*resultsDir, "run_meta.json")
	if err := WriteRunMeta(metaPath, meta); err != nil {
		log.Fatalf("write run meta: %v", err)
	}

	// Open predictions writer.
	rw, err := NewResultsWriter(*output, *model)
	if err != nil {
		log.Fatalf("open results writer: %v", err)
	}
	defer rw.Close()

	// Create Docker runner.
	docker := &DockerRunner{
		Image:    *dockerImage,
		CacheDir: cacheDir,
		Timeout:  *timeout,
	}

	// Build base agent environment map.
	agentEnv := map[string]string{
		"SWEBENCH_MODEL":     *model,
		"SWEBENCH_PROVIDER":  *provider,
		"SWEBENCH_MAX_TURNS": fmt.Sprintf("%d", *maxTurns),
		"SWEBENCH_TIMEOUT":   timeout.String(),
	}
	if *gatewayURL != "" {
		agentEnv["TRACKER_GATEWAY_URL"] = *gatewayURL
	}
	// Pass through API keys from host environment.
	if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" {
		agentEnv["ANTHROPIC_API_KEY"] = v
	}
	if v := os.Getenv("OPENAI_API_KEY"); v != "" {
		agentEnv["OPENAI_API_KEY"] = v
	}

	// Handle Ctrl+C gracefully.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	stats := RunStats{
		Total:     len(instances),
		StartTime: time.Now(),
	}

	total := len(instances)
	for i, inst := range instances {
		// Check for cancellation.
		select {
		case <-ctx.Done():
			log.Println("interrupted, stopping run")
			fmt.Println(stats.Summary())
			return
		default:
		}

		// Skip already-completed instances unless --force.
		if !*force && rw.IsCompleted(inst.InstanceID) {
			stats.Skipped++
			fmt.Printf("[%d/%d] %s ... skipped (already completed)\n", i+1, total, inst.InstanceID)
			continue
		}

		// Ensure bare clone is cached for the repo.
		repoCachePath := filepath.Join(cacheDir, strings.ReplaceAll(inst.Repo, "/", "_")+".git")
		if err := ensureBareClone(ctx, inst.RepoURL(), repoCachePath); err != nil {
			log.Printf("[%s] bare clone failed (continuing without cache): %v", inst.InstanceID, err)
			repoCachePath = "" // fall back to no cache
		}

		// Set per-instance env.
		instEnv := make(map[string]string, len(agentEnv)+1)
		for k, v := range agentEnv {
			instEnv[k] = v
		}
		instEnv["SWEBENCH_INSTANCE"] = inst.AgentPrompt()

		start := time.Now()
		patch, summary, runErr := docker.RunInstance(ctx, inst, instEnv)
		elapsed := time.Since(start).Round(time.Second)

		// Write prediction even on error (capture partial patch).
		if writeErr := rw.WritePrediction(inst.InstanceID, patch); writeErr != nil {
			log.Printf("[%s] write prediction: %v", inst.InstanceID, writeErr)
		}

		// Write per-instance log.
		logPath := filepath.Join(logsDir, inst.InstanceID+".log")
		logContent := fmt.Sprintf("instance_id: %s\nelapsed: %s\nturns: %d\ninput_tokens: %d\noutput_tokens: %d\npatch_lines: %d\n",
			inst.InstanceID, elapsed, summary.Turns, summary.InputTokens, summary.OutputTokens, patchLineCount(patch))
		if runErr != nil {
			logContent += fmt.Sprintf("error: %v\n", runErr)
		}
		if writeErr := os.WriteFile(logPath, []byte(logContent), 0o644); writeErr != nil {
			log.Printf("[%s] write log: %v", inst.InstanceID, writeErr)
		}

		// Update stats.
		stats.Completed++
		stats.InputTokens += summary.InputTokens
		stats.OutputTokens += summary.OutputTokens
		if patch != "" {
			stats.Patched++
		}
		if runErr != nil && strings.Contains(runErr.Error(), "context deadline exceeded") {
			stats.TimedOut++
		}

		// Print progress line.
		fmt.Printf("[%d/%d] %s ... %d turns, %s, patch: %d lines\n",
			i+1, total, inst.InstanceID, summary.Turns, elapsed, patchLineCount(patch))
	}

	fmt.Println(stats.Summary())
}

// ensureBareClone clones repoURL as a bare repo to path if path does not already exist.
func ensureBareClone(ctx context.Context, repoURL, path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil // already cached
	}

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", "clone", "--bare", repoURL, path)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone --bare %s: %w\nstderr: %s", repoURL, err, stderr.String())
	}
	return nil
}
