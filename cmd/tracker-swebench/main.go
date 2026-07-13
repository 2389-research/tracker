// ABOUTME: CLI entry point for the tracker-swebench SWE-bench benchmarking harness.
// ABOUTME: Runs tracker's code agent against SWE-bench Lite instances and records predictions.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"maps"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"
)

// runConfig holds the resolved benchmark run flags.
type runConfig struct {
	dataset     string
	model       string
	provider    string
	gatewayURL  string
	output      string
	resultsDir  string
	instance    string
	dockerImage string
	maxTurns    int
	timeout     time.Duration
	force       bool
}

// runDeps bundles the shared state a single instance run needs.
type runDeps struct {
	rw            *ResultsWriter
	docker        *DockerRunner
	agentEnv      map[string]string
	absResultsDir string
	logsDir       string
	cacheDir      string
	force         bool
}

func main() {
	// Subcommand dispatch: the "analyze" verb routes to its handler and exits.
	// Everything else falls through to the default run flow so that existing
	// invocations like `tracker-swebench --dataset ...` keep working.
	if len(os.Args) > 1 && os.Args[1] == "analyze" {
		if err := runAnalyze(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	cfg, run := parseRunFlags()
	if !run {
		return // the bare "help" verb was given; usage already printed
	}
	if err := runBenchmark(cfg); err != nil {
		os.Exit(1)
	}
}

// parseRunFlags registers and parses the benchmark run flags. It returns the
// resolved config and true when the caller should proceed, or (nil, false)
// when the bare "help" verb was given and usage was printed.
func parseRunFlags() (*runConfig, bool) {
	// The bare `help` verb is handled here (not by flag.Parse, which only
	// recognizes -h/--help as flags) so that flag.Usage sees the full flag set.
	helpRequested := len(os.Args) > 1 && os.Args[1] == "help"

	dataset := flag.String("dataset", "", "path to JSONL file (required)")
	model := flag.String("model", "claude-sonnet-4-6", "model name")
	provider := flag.String("provider", "anthropic", "provider name")
	gatewayURL := flag.String("gateway-url", "", "Cloudflare AI Gateway URL")
	output := flag.String("output", "./predictions.jsonl", "output file for predictions")
	resultsDir := flag.String("results-dir", "./results", "results directory")
	maxTurns := flag.Int("max-turns", 50, "maximum agent turns per instance")
	timeout := flag.Duration("timeout", 30*time.Minute, "per-instance timeout")
	instance := flag.String("instance", "", "single instance filter (optional)")
	force := flag.Bool("force", false, "re-run completed instances")
	dockerImage := flag.String("docker-image", "tracker-swebench-base", "Docker image to use")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `tracker-swebench — run tracker's code agent against SWE-bench Lite instances

Usage:
  tracker-swebench --dataset <path> [flags]          # run a benchmark
  tracker-swebench analyze <results-dir> [flags]     # triage a completed run

Prerequisites:
  1. Build the Docker image:  cd cmd/tracker-swebench && bash build.sh
  2. Set API key:             export ANTHROPIC_API_KEY=sk-ant-...
  3. Download dataset:        SWE-bench Lite JSONL from the SWE-bench repository

Examples:
  tracker-swebench --dataset swebench_lite.jsonl
  tracker-swebench --dataset swebench_lite.jsonl --instance django__django-11099
  tracker-swebench --dataset swebench_lite.jsonl --model gpt-5.2 --provider openai
  tracker-swebench --dataset swebench_lite.jsonl --force --timeout 30m
  tracker-swebench analyze ./results
  tracker-swebench analyze ./results --json > report.json

Flags:
`)
		flag.PrintDefaults()
	}

	if helpRequested {
		flag.Usage()
		return nil, false
	}

	flag.Parse()

	if *dataset == "" {
		log.Fatal("--dataset is required")
	}

	return &runConfig{
		dataset:     *dataset,
		model:       *model,
		provider:    *provider,
		gatewayURL:  *gatewayURL,
		output:      *output,
		resultsDir:  *resultsDir,
		instance:    *instance,
		dockerImage: *dockerImage,
		maxTurns:    *maxTurns,
		timeout:     *timeout,
		force:       *force,
	}, true
}

// runBenchmark runs the agent over every instance in the dataset, writing
// predictions and per-instance logs. It returns a non-nil error when any
// instance errored, so the caller can exit non-zero.
func runBenchmark(cfg *runConfig) error {
	instances := loadInstances(cfg)

	absResultsDir, logsDir, cacheDir := setupResultsDirs(cfg.resultsDir)
	writeRunMetadata(cfg, absResultsDir)

	rw, err := NewResultsWriter(cfg.output, cfg.model)
	if err != nil {
		log.Fatalf("open results writer: %v", err)
	}
	defer rw.Close()

	// Handle Ctrl+C and SIGTERM gracefully.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	docker := &DockerRunner{
		Image:     cfg.dockerImage,
		CacheDir:  cacheDir,
		Timeout:   cfg.timeout,
		MemoryMB:  4096, // 4 GB
		CPUs:      1.0,
		PidsLimit: 512,
		RunLabel:  time.Now().Format("20060102-150405"),
	}
	// Clean up orphaned containers from prior crashed runs.
	docker.CleanupStale(ctx)

	deps := &runDeps{
		rw:            rw,
		docker:        docker,
		agentEnv:      buildAgentEnv(cfg),
		absResultsDir: absResultsDir,
		logsDir:       logsDir,
		cacheDir:      cacheDir,
		force:         cfg.force,
	}

	stats := RunStats{Total: len(instances), StartTime: time.Now()}
	total := len(instances)
	for i, inst := range instances {
		select {
		case <-ctx.Done():
			log.Println("interrupted, stopping run")
			fmt.Println(stats.Summary())
			return nil
		default:
		}
		processInstance(ctx, i, total, inst, deps, &stats)
	}

	fmt.Println(stats.Summary())
	if stats.Errors > 0 {
		return fmt.Errorf("%d instance(s) errored", stats.Errors)
	}
	return nil
}

// loadInstances loads the dataset and applies the optional single-instance
// filter.
func loadInstances(cfg *runConfig) []Instance {
	instances, err := LoadDataset(cfg.dataset)
	if err != nil {
		log.Fatalf("load dataset: %v", err)
	}
	if cfg.instance != "" {
		instances = filterToInstance(instances, cfg.instance)
	}
	return instances
}

// filterToInstance narrows instances to the single one named, exiting if it is
// not present.
func filterToInstance(instances []Instance, name string) []Instance {
	for _, inst := range instances {
		if inst.InstanceID == name {
			return []Instance{inst}
		}
	}
	log.Fatalf("instance %q not found in dataset", name)
	return nil
}

// setupResultsDirs resolves and creates the results, logs, and cache dirs.
// Docker -v requires absolute paths.
func setupResultsDirs(resultsDir string) (absResultsDir, logsDir, cacheDir string) {
	absResultsDir, err := filepath.Abs(resultsDir)
	if err != nil {
		log.Fatalf("resolve results dir: %v", err)
	}
	logsDir = filepath.Join(absResultsDir, "logs")
	cacheDir = filepath.Join(absResultsDir, "repo-cache")
	for _, dir := range []string{absResultsDir, logsDir, cacheDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("create dir %q: %v", dir, err)
		}
	}
	return absResultsDir, logsDir, cacheDir
}

// writeRunMetadata records run metadata to run_meta.json.
func writeRunMetadata(cfg *runConfig, absResultsDir string) {
	// Record which base URL override is active (if any). Normalize hyphens to
	// underscores before uppercasing so providers like "openai-compat" map to
	// OPENAI_COMPAT_BASE_URL, matching how ResolveProviderBaseURL derives keys.
	baseURLOverride := os.Getenv(strings.ToUpper(strings.ReplaceAll(cfg.provider, "-", "_")) + "_BASE_URL")
	meta := RunMeta{
		Model:           cfg.model,
		Provider:        cfg.provider,
		GatewayURL:      cfg.gatewayURL,
		BaseURLOverride: baseURLOverride,
		Dataset:         cfg.dataset,
		MaxTurns:        cfg.maxTurns,
		Timeout:         cfg.timeout.String(),
		Commit:          buildCommit(),
	}
	metaPath := filepath.Join(absResultsDir, "run_meta.json")
	if err := WriteRunMeta(metaPath, meta); err != nil {
		log.Fatalf("write run meta: %v", err)
	}
}

// buildAgentEnv builds the base agent environment map: the SWEBENCH_* config
// plus any API keys and base-URL overrides passed through from the host.
func buildAgentEnv(cfg *runConfig) map[string]string {
	agentEnv := map[string]string{
		"SWEBENCH_MODEL":     cfg.model,
		"SWEBENCH_PROVIDER":  cfg.provider,
		"SWEBENCH_MAX_TURNS": fmt.Sprintf("%d", cfg.maxTurns),
		"SWEBENCH_TIMEOUT":   cfg.timeout.String(),
	}
	if cfg.gatewayURL != "" {
		agentEnv["TRACKER_GATEWAY_URL"] = cfg.gatewayURL
	}
	// Pass through API keys and provider base-URL overrides from the host env.
	for _, key := range []string{
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "CF_AIG_TOKEN",
		"ANTHROPIC_BASE_URL", "OPENAI_BASE_URL",
	} {
		if v := os.Getenv(key); v != "" {
			agentEnv[key] = v
		}
	}
	return agentEnv
}

// processInstance runs a single instance end to end: skip check, repo cache,
// prompt file, container run, and prediction/log/stats bookkeeping.
func processInstance(ctx context.Context, idx, total int, inst Instance, d *runDeps, stats *RunStats) {
	// Skip already-completed instances unless --force.
	if !d.force && d.rw.IsCompleted(inst.InstanceID) {
		stats.Skipped++
		fmt.Printf("[%d/%d] %s ... skipped (already completed)\n", idx+1, total, inst.InstanceID)
		return
	}

	repoCachePath := ensureRepoCache(ctx, d.cacheDir, inst)

	promptPath, err := writePromptFile(d.absResultsDir, inst)
	if err != nil {
		log.Printf("[%s] create prompt file: %v", inst.InstanceID, err)
		stats.addError(runErrorHarness)
		return
	}

	start := time.Now()
	patch, summary, transcript, runErr := d.docker.RunInstance(ctx, inst, cloneEnv(d.agentEnv), repoCachePath != "", promptPath)
	os.Remove(promptPath)
	elapsed := time.Since(start).Round(time.Second)

	// Write prediction even on error (capture partial patch).
	if writeErr := d.rw.WritePrediction(inst.InstanceID, patch); writeErr != nil {
		log.Printf("[%s] write prediction: %v", inst.InstanceID, writeErr)
		stats.addError(runErrorHarness)
		writeInstanceRunLog(d.logsDir, inst, elapsed, summary, patch, runErr, writeErr)
		return
	}

	writeInstanceRunLog(d.logsDir, inst, elapsed, summary, patch, runErr, nil)
	writeArtifacts(d.logsDir, inst, transcript, patch, summary)
	updateStats(stats, summary, patch, runErr)

	fmt.Printf("[%d/%d] %s ... %d turns, %s, patch: %d lines\n",
		idx+1, total, inst.InstanceID, summary.Turns, elapsed, patchLineCount(patch))
}

// ensureRepoCache ensures a bare clone of the instance repo exists, returning
// its path or "" to fall back to no cache.
func ensureRepoCache(ctx context.Context, cacheDir string, inst Instance) string {
	repoCachePath := filepath.Join(cacheDir, strings.ReplaceAll(inst.Repo, "/", "_")+".git")
	if err := ensureBareClone(ctx, inst.RepoURL(), repoCachePath); err != nil {
		log.Printf("[%s] bare clone failed (continuing without cache): %v", inst.InstanceID, err)
		return "" // fall back to no cache
	}
	return repoCachePath
}

// cloneEnv returns a shallow copy of src so per-instance runs cannot mutate the
// shared base environment.
func cloneEnv(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	maps.Copy(dst, src)
	return dst
}

// writePromptFile writes the instance prompt to a temp file under dir for
// mounting into the container. The prompt can be multiline and contain special
// chars that break Docker's --env-file format, so it is passed as a file. The
// file is created under the run's results dir to keep artifacts co-located.
func writePromptFile(dir string, inst Instance) (string, error) {
	promptFile, err := os.CreateTemp(dir, "swebench-prompt-*")
	if err != nil {
		return "", err
	}
	if _, err := promptFile.WriteString(inst.AgentPrompt()); err != nil {
		promptFile.Close()
		os.Remove(promptFile.Name())
		return "", err
	}
	promptFile.Close()
	return promptFile.Name(), nil
}

// writeInstanceLog writes the per-instance summary log. predErr, when non-nil,
// records a prediction-write failure in the log body.
func writeInstanceRunLog(logsDir string, inst Instance, elapsed time.Duration, summary AgentSummary, patch string, runErr, predErr error) {
	logContent := fmt.Sprintf("instance_id: %s\nelapsed: %s\nturns: %d\ninput_tokens: %d\noutput_tokens: %d\npatch_lines: %d\n",
		inst.InstanceID, elapsed, summary.Turns, summary.InputTokens, summary.OutputTokens, patchLineCount(patch))
	if predErr != nil {
		logContent += fmt.Sprintf("write_prediction_error: %v\n", predErr)
	}
	if runErr != nil {
		logContent += fmt.Sprintf("error: %v\n", runErr)
	}
	logPath := filepath.Join(logsDir, inst.InstanceID+".log")
	if err := os.WriteFile(logPath, []byte(logContent), 0o644); err != nil {
		log.Printf("[%s] write log: %v", inst.InstanceID, err)
	}
}

// writeArtifacts writes the agent transcript and, for empty patches, the
// empty-patch diagnostic for post-mortem analysis.
func writeArtifacts(logsDir string, inst Instance, transcript, patch string, summary AgentSummary) {
	if transcript != "" {
		transcriptPath := filepath.Join(logsDir, inst.InstanceID+".transcript.log")
		if err := os.WriteFile(transcriptPath, []byte(transcript), 0o644); err != nil {
			log.Printf("[%s] write transcript: %v", inst.InstanceID, err)
		}
	}
	if patch == "" {
		diag := EmptyPatchDiagnostic{
			InstanceID:        inst.InstanceID,
			Turns:             summary.Turns,
			TerminationReason: summary.TerminationReason,
			FinalMessage:      summary.FinalMessage,
			LastToolCalls:     summary.LastToolCalls,
		}
		if err := WriteEmptyPatchDiagnostic(logsDir, diag); err != nil {
			log.Printf("[%s] write empty patch diagnostic: %v", inst.InstanceID, err)
		}
	}
}

// updateStats folds one instance's result into the running totals.
func updateStats(stats *RunStats, summary AgentSummary, patch string, runErr error) {
	stats.Completed++
	stats.InputTokens += summary.InputTokens
	stats.OutputTokens += summary.OutputTokens
	if patch != "" {
		stats.Patched++
	}
	if runErr != nil {
		stats.addError(classifyRunError(runErr))
		if errors.Is(runErr, context.DeadlineExceeded) {
			stats.TimedOut++
		}
	}
}

// buildCommit returns the VCS revision from Go build info, or "unknown".
func buildCommit() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			if len(s.Value) > 12 {
				return s.Value[:12]
			}
			return s.Value
		}
	}
	return "unknown"
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
