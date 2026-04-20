// ABOUTME: In-container agent binary for SWE-bench evaluation harness.
// ABOUTME: Creates an agent.Session directly to fix GitHub issues inside Docker containers.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	tracker "github.com/2389-research/tracker"
	"github.com/2389-research/tracker/agent"
	agentexec "github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/llm"
)

const swebenchSystemPrompt = `You are an expert software engineer tasked with fixing a GitHub issue.

You have access to the repository at /workspace. The repository is already
checked out at the correct commit.

## Your task
Fix the issue described below. Make the minimal changes necessary to resolve
the issue. Do not refactor unrelated code.

## Approach
1. Read the issue carefully. Understand what's broken and what the expected behavior is.
2. Explore the relevant code. Use grep_search and glob to find the right files.
3. Read the relevant test files to understand what the tests expect.
4. Check git log --oneline -10 for recent context around the affected code.
5. Write a fix. Make targeted edits — smallest diff that solves the problem.
6. Run the failing test to verify your fix: python -m pytest <test_file> -x --tb=short
7. Run the broader test module to check for regressions: python -m pytest <test_dir> -x --tb=short
8. If tests fail, read the error carefully, fix, and re-run.

## Rules
- Do NOT create new test files. The evaluation uses the repo's existing test suite.
- Do NOT modify test files unless the issue specifically requires it.
- Keep your changes minimal and focused.
- If you're unsure about the fix, read more code before editing.
- Always re-read a file before editing it if you haven't read it recently.
- After editing, verify the fix by running the specific failing test AND the broader test module.
- You may use absolute paths in bash commands (e.g., /workspace/tests/).

## Anti-thrashing
- If you've been exploring for 20+ turns without a fix, commit to your best candidate and test it.
- Don't keep searching for a "perfect" solution — apply your best fix and iterate from test feedback.
- Read test files BEFORE implementing — understand the expected interface/behavior.
- When tests fail after your fix, read the FULL error output before making more changes.`

type runnerConfig struct {
	Instance string
	RepoDir  string
	Model    string
	Provider string
	MaxTurns int
	Timeout  time.Duration
}

// parseConfig reads runner configuration from environment variables,
// falling back to sensible defaults for all optional fields.
func parseConfig() runnerConfig {
	cfg := runnerConfig{
		Instance: os.Getenv("SWEBENCH_INSTANCE"),
		RepoDir:  "/workspace",
		Model:    "claude-sonnet-4-6",
		Provider: "anthropic",
		MaxTurns: 80,
		Timeout:  30 * time.Minute,
	}

	if v := os.Getenv("SWEBENCH_REPO_DIR"); v != "" {
		cfg.RepoDir = v
	}
	if v := os.Getenv("SWEBENCH_MODEL"); v != "" {
		cfg.Model = v
	}
	if v := os.Getenv("SWEBENCH_PROVIDER"); v != "" {
		cfg.Provider = v
	}
	if v := os.Getenv("SWEBENCH_MAX_TURNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxTurns = n
		}
	}
	if v := os.Getenv("SWEBENCH_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Timeout = d
		}
	}

	return cfg
}

type agentSummary struct {
	Turns        int   `json:"turns"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	DurationMs   int64 `json:"duration_ms"`
}

// instancePromptPath is the container-side path where the harness mounts
// the instance prompt file. Used instead of env vars because multiline
// prompts break Docker's --env-file format.
const instancePromptPath = "/instance_prompt.txt"

func main() {
	cfg := parseConfig()
	if cfg.Instance == "" {
		// Fall back to reading from the mounted prompt file.
		data, err := os.ReadFile(instancePromptPath)
		if err != nil {
			log.Fatalf("SWEBENCH_INSTANCE not set and %s not readable: %v", instancePromptPath, err)
		}
		cfg.Instance = string(data)
	}
	if cfg.Instance == "" {
		log.Fatal("SWEBENCH_INSTANCE must be set or /instance_prompt.txt must exist")
	}

	baseURL := tracker.ResolveProviderBaseURL(cfg.Provider)

	client, err := buildLLMClient(cfg.Provider, baseURL)
	if err != nil {
		log.Fatalf("failed to build LLM client: %v", err)
	}
	defer client.Close()

	sessionCfg := agent.DefaultConfig()
	sessionCfg.Model = cfg.Model
	sessionCfg.Provider = cfg.Provider
	sessionCfg.MaxTurns = cfg.MaxTurns
	sessionCfg.CommandTimeout = 30 * time.Second
	sessionCfg.MaxCommandTimeout = 5 * time.Minute
	sessionCfg.ContextCompaction = agent.CompactionAuto
	sessionCfg.CompactionThreshold = 0.7
	sessionCfg.ReflectOnError = true
	sessionCfg.VerifyAfterEdit = true
	sessionCfg.VerifyCommand = "set -o pipefail; python -m pytest --tb=short -q -x 2>&1 | tail -50"
	sessionCfg.VerifyBroadCommand = "set -o pipefail; python -m pytest --tb=short -q 2>&1 | tail -100"
	sessionCfg.Checkpoints = []agent.Checkpoint{
		{
			Fraction: 0.5,
			Message: `CHECKPOINT: You've used half your turn budget. Before continuing:
1. List what approaches you've tried so far and their results.
2. If none of your approaches have made the failing tests pass, STOP and re-read the issue description and test file from scratch.
3. Consider whether you're editing the right file/function. The fix might be elsewhere.
4. Commit to ONE approach and test it thoroughly before trying alternatives.`,
		},
		{
			Fraction: 0.75,
			Message: `URGENT: You have 25% of your turn budget remaining.
1. If you have a partially working fix, focus on making it complete.
2. If nothing has worked, apply your best-guess minimal fix NOW and run the tests.
3. Do NOT start exploring new files or refactoring — focus on testing what you have.`,
		},
	}
	sessionCfg.WorkingDir = cfg.RepoDir
	sessionCfg.SystemPrompt = swebenchSystemPrompt

	env := agentexec.NewLocalEnvironment(cfg.RepoDir)

	// Log agent events to stderr for transcript visibility.
	evtHandler := agent.EventHandlerFunc(func(evt agent.Event) {
		log.Printf("[agent:%s] turn=%d %s", evt.Type, evt.Turn, evt.Text)
	})

	sess, err := agent.NewSession(client, sessionCfg, agent.WithEnvironment(env), agent.WithEventHandler(evtHandler))
	if err != nil {
		log.Fatalf("failed to create agent session: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	start := time.Now()
	result, err := sess.Run(ctx, cfg.Instance)
	elapsed := time.Since(start)

	summary := agentSummary{
		DurationMs: elapsed.Milliseconds(),
	}
	if result.Turns > 0 {
		summary.Turns = result.Turns
		summary.InputTokens = int64(result.Usage.InputTokens)
		summary.OutputTokens = int64(result.Usage.OutputTokens)
	}

	enc := json.NewEncoder(os.Stdout)
	if encErr := enc.Encode(summary); encErr != nil {
		log.Printf("failed to encode summary: %v", encErr)
	}

	if err != nil {
		log.Fatalf("agent session failed: %v", err)
	}
}

// buildLLMClient creates a single-provider LLM client with retry middleware.
func buildLLMClient(provider, baseURL string) (*llm.Client, error) {
	// Validate provider upfront.
	switch provider {
	case "anthropic", "openai":
		// supported
	default:
		return nil, fmt.Errorf("unsupported provider %q: must be \"anthropic\" or \"openai\"", provider)
	}

	constructors := map[string]func(string) (llm.ProviderAdapter, error){
		provider: func(key string) (llm.ProviderAdapter, error) {
			switch provider {
			case "openai":
				return newOpenAIAdapter(key, baseURL)
			default:
				return newAnthropicAdapter(key, baseURL)
			}
		},
	}

	client, err := llm.NewClientFromEnv(constructors)
	if err != nil {
		return nil, err
	}

	client.AddMiddleware(llm.NewRetryMiddleware(
		llm.WithMaxRetries(3),
		llm.WithBaseDelay(2*time.Second),
	))

	return client, nil
}
