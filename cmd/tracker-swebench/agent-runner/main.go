// ABOUTME: agent-runner binary for executing a tracker coding agent inside a SWE-bench Docker container.
// ABOUTME: Reads config from environment variables, runs the agent against a repo, and writes a JSON summary.
package main

import (
	"os"
	"strconv"
	"time"
)

// swebenchSystemPrompt is the system prompt given to the coding agent for SWE-bench tasks.
const swebenchSystemPrompt = `You are an expert software engineer solving a GitHub issue.
You have access to the repository checked out at the configured repo directory.
Read the problem statement carefully, explore the codebase, reproduce the issue if possible,
implement a minimal fix, and verify it with existing tests. Do not add unnecessary changes.
Focus only on the specific issue described. When done, your changes will be captured as a git diff.`

// agentConfig holds the runtime configuration for one agent-runner invocation.
type agentConfig struct {
	Instance string
	RepoDir  string
	Model    string
	Provider string
	MaxTurns int
	Timeout  time.Duration
	Prompt   string
}

// parseConfig reads agent configuration from environment variables with sensible defaults.
func parseConfig() agentConfig {
	cfg := agentConfig{
		Instance: os.Getenv("SWEBENCH_INSTANCE"),
		RepoDir:  envOrDefault("SWEBENCH_REPO_DIR", "/workspace"),
		Model:    envOrDefault("SWEBENCH_MODEL", "claude-sonnet-4-6"),
		Provider: envOrDefault("SWEBENCH_PROVIDER", "anthropic"),
		MaxTurns: envIntOrDefault("SWEBENCH_MAX_TURNS", 50),
		Timeout:  envDurationOrDefault("SWEBENCH_TIMEOUT", 10*time.Minute),
		Prompt:   os.Getenv("SWEBENCH_PROMPT"),
	}
	return cfg
}

// envOrDefault returns the value of the named env var, or def if empty.
func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envIntOrDefault returns the int value of the named env var, or def if empty or invalid.
func envIntOrDefault(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// envDurationOrDefault returns the duration value of the named env var, or def if empty or invalid.
func envDurationOrDefault(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func main() {
	// TODO: implement full agent-runner execution loop
}
