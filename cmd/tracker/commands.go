// ABOUTME: Command dispatch and shared utilities for the tracker CLI.
// ABOUTME: Routes subcommands, resolves checkpoints, and manages .env loading.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/2389-research/tracker/pipeline/handlers"
	"github.com/joho/godotenv"
	"github.com/mattn/go-isatty"
)

func executeCommand(cfg runConfig, deps commandDeps) error {
	if deps.loadEnv == nil {
		deps.loadEnv = loadEnvFiles
	}
	if deps.runSetup == nil {
		deps.runSetup = runSetup
	}
	if deps.run == nil {
		deps.run = run
	}
	if deps.runTUI == nil {
		deps.runTUI = runTUI
	}

	switch cfg.mode {
	case modeVersion:
		return executeVersion()
	case modeDiagnose:
		return executeDiagnose(cfg)
	case modeDoctor:
		return executeDoctor(cfg)
	case modeSetup:
		return deps.runSetup()
	case modeValidate:
		return executeValidate(cfg)
	case modeSimulate:
		return executeSimulate(cfg)
	case modeAudit:
		return executeAudit(cfg)
	case modeWorkflows:
		return executeWorkflows()
	case modeInit:
		return executeInit(cfg)
	case modeUpdate:
		return executeUpdate()
	default:
		return executeRun(cfg, deps)
	}
}

func executeVersion() error {
	// Load env so provider status reflects .env files.
	wd, _ := os.Getwd()
	_ = loadEnvFiles(wd)

	fmt.Printf("tracker %s\n", version)
	fmt.Printf("  commit: %s\n", commit)
	fmt.Printf("  built:  %s\n", date)
	printProviderStatus()
	return nil
}

func executeDiagnose(cfg runConfig) error {
	if cfg.resumeID == "" {
		// No run ID provided — diagnose the most recent run.
		return diagnoseMostRecent(cfg.workdir)
	}
	return runDiagnose(cfg.workdir, cfg.resumeID)
}

func executeDoctor(cfg runConfig) error {
	_ = loadEnvFiles(cfg.workdir)
	return runDoctor(cfg.workdir)
}

// printProviderStatus shows which LLM providers have API keys configured.
func printProviderStatus() {
	providers := []struct {
		name string
		envs []string
	}{
		{"anthropic", []string{"ANTHROPIC_API_KEY"}},
		{"openai", []string{"OPENAI_API_KEY"}},
		{"gemini", []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}},
	}
	var ready, missing []string
	for _, p := range providers {
		found := false
		for _, e := range p.envs {
			if os.Getenv(e) != "" {
				found = true
				break
			}
		}
		if found {
			ready = append(ready, p.name)
		} else {
			missing = append(missing, p.name)
		}
	}
	if len(ready) > 0 {
		fmt.Printf("  providers: %s\n", strings.Join(ready, ", "))
	} else {
		fmt.Println("  providers: none (run `tracker setup`)")
	}
}

func executeWorkflows() error {
	workflows := listBuiltinWorkflows()
	if len(workflows) == 0 {
		fmt.Println("No built-in workflows available.")
		return nil
	}

	fmt.Println("\nBuilt-in workflows:")
	fmt.Println()
	fmt.Printf("  %-35s  %s\n", "NAME", "DESCRIPTION")
	fmt.Printf("  %-35s  %s\n", "────", "───────────")
	for _, wf := range workflows {
		goal := wf.Goal
		if len(goal) > 80 {
			goal = goal[:77] + "..."
		}
		fmt.Printf("  %-35s  %s\n", wf.Name+" ("+wf.DisplayName+")", goal)
	}
	fmt.Println()
	fmt.Println("  Run directly:     tracker <workflow_name>")
	fmt.Println("  Copy to edit:     tracker init <workflow_name>")
	fmt.Println("  Validate:         tracker validate <workflow_name>")
	fmt.Println()
	return nil
}

func executeInit(cfg runConfig) error {
	if cfg.pipelineFile == "" {
		workflows := listBuiltinWorkflows()
		fmt.Fprintf(os.Stderr, "Usage: tracker init <workflow_name>\n\nAvailable workflows:\n")
		for _, wf := range workflows {
			fmt.Fprintf(os.Stderr, "  %s\n", wf.Name)
		}
		return fmt.Errorf("workflow name required")
	}

	info, ok := lookupBuiltinWorkflow(cfg.pipelineFile)
	if !ok {
		workflows := listBuiltinWorkflows()
		var names []string
		for _, wf := range workflows {
			names = append(names, wf.Name)
		}
		return fmt.Errorf("unknown workflow %q (available: %s)", cfg.pipelineFile, strings.Join(names, ", "))
	}

	outFile := info.Name + ".dip"
	if _, err := os.Stat(outFile); err == nil {
		return fmt.Errorf("%s already exists — remove it first or edit it directly", outFile)
	}

	data, err := embeddedWorkflows.ReadFile(info.File)
	if err != nil {
		return fmt.Errorf("read embedded workflow: %w", err)
	}

	if err := os.WriteFile(outFile, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outFile, err)
	}

	fmt.Printf("Created %s — edit it, then run with: tracker %s\n", outFile, outFile)
	return nil
}

func executeValidate(cfg runConfig) error {
	if cfg.pipelineFile == "" {
		return fmt.Errorf("usage: tracker validate <pipeline.dip>")
	}
	return runValidateCmd(cfg.pipelineFile, cfg.format, os.Stdout)
}

func executeSimulate(cfg runConfig) error {
	if cfg.pipelineFile == "" {
		return fmt.Errorf("usage: tracker simulate <pipeline.dip>")
	}
	return runSimulateCmd(cfg.pipelineFile, cfg.format, os.Stdout)
}

func executeAudit(cfg runConfig) error {
	if cfg.resumeID == "" {
		return listRuns(cfg.workdir)
	}
	return runAudit(cfg.workdir, cfg.resumeID)
}

func executeRun(cfg runConfig, deps commandDeps) error {
	if err := deps.loadEnv(cfg.workdir); err != nil {
		return err
	}

	// Store autopilot config for chooseInterviewer (called from run/runTUI).
	activeAutopilotCfg = autopilotCfg{persona: cfg.autopilot, autoApprove: cfg.autoApprove}

	if cfg.autopilot != "" {
		if _, err := handlers.ParsePersona(cfg.autopilot); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Running in autopilot mode (persona: %s) — human gates answered by LLM\n", cfg.autopilot)
	} else if cfg.autoApprove {
		fmt.Fprintln(os.Stderr, "Running in auto-approve mode — all human gates auto-approved")
	}

	printStartupBanner()

	// Resolve run ID to checkpoint path.
	checkpoint := ""
	if cfg.resumeID != "" {
		cp, err := resolveCheckpoint(cfg.workdir, cfg.resumeID)
		if err != nil {
			return err
		}
		checkpoint = cp
	}

	// JSON streaming and autopilot modes force non-TUI.
	if cfg.jsonOut || cfg.autopilot != "" || cfg.autoApprove {
		cfg.noTUI = true
	}

	// Fall back to plain console mode when TUI is disabled or stdin is not a
	// terminal (e.g. CI, piped input, cron). TUI requires a real TTY.
	if cfg.noTUI || !isatty.IsTerminal(os.Stdin.Fd()) {
		return deps.run(cfg.pipelineFile, cfg.workdir, checkpoint, cfg.format, cfg.backend, cfg.verbose, cfg.jsonOut)
	}
	return deps.runTUI(cfg.pipelineFile, cfg.workdir, checkpoint, cfg.format, cfg.backend, cfg.verbose)
}

// resolveCheckpoint finds the checkpoint file for a given run ID. It looks in
// .tracker/runs/<runID>/checkpoint.json under the working directory. If the ID
// is a prefix that uniquely matches one run, it resolves to that run.
func resolveCheckpoint(workdir, runID string) (string, error) {
	if runID == "" {
		return "", fmt.Errorf("run ID cannot be empty")
	}
	runsDir := filepath.Join(workdir, ".tracker", "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return "", fmt.Errorf("cannot read runs directory: %w", err)
	}

	var matches []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), runID) {
			matches = append(matches, e.Name())
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no run found matching %q in %s", runID, runsDir)
	case 1:
		// Unique match (exact or prefix)
	default:
		// Check for exact match among the prefix matches
		exact := false
		for _, m := range matches {
			if m == runID {
				matches = []string{m}
				exact = true
				break
			}
		}
		if !exact {
			return "", fmt.Errorf("ambiguous run ID %q matches %d runs: %s", runID, len(matches), strings.Join(matches, ", "))
		}
	}

	cpPath := filepath.Join(runsDir, matches[0], "checkpoint.json")
	if _, err := os.Stat(cpPath); err != nil {
		return "", fmt.Errorf("checkpoint not found for run %s: %w", matches[0], err)
	}
	return cpPath, nil
}

func loadEnvFiles(workdir string) error {
	originalEnv := currentEnvKeys()

	configEnvPath, err := resolveConfigEnvPath()
	if err != nil {
		return fmt.Errorf("resolve XDG config dir: %w", err)
	}
	if err := loadEnvFileIfPresent(configEnvPath, originalEnv); err != nil {
		return err
	}

	localEnvPath := filepath.Join(workdir, ".env")
	if err := loadEnvFileIfPresent(localEnvPath, originalEnv); err != nil {
		return err
	}

	return nil
}

func currentEnvKeys() map[string]struct{} {
	keys := make(map[string]struct{})
	for _, entry := range os.Environ() {
		if idx := strings.IndexByte(entry, '='); idx > 0 {
			keys[entry[:idx]] = struct{}{}
		}
	}
	return keys
}

func loadEnvFileIfPresent(path string, originalEnv map[string]struct{}) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat env file %s: %w", path, err)
	}

	values, err := godotenv.Read(path)
	if err != nil {
		return fmt.Errorf("load env file %s: %w", path, err)
	}

	for key, value := range values {
		if _, exists := originalEnv[key]; exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set env %s from %s: %w", key, path, err)
		}
	}
	return nil
}

func envMapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}
