// ABOUTME: Command dispatch and shared utilities for the tracker CLI.
// ABOUTME: Routes subcommands, resolves checkpoints, and manages .env loading.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	case modeSetup:
		return deps.runSetup()
	case modeValidate:
		return executeValidate(cfg)
	case modeSimulate:
		return executeSimulate(cfg)
	case modeAudit:
		return executeAudit(cfg)
	default:
		return executeRun(cfg, deps)
	}
}

func executeVersion() error {
	fmt.Printf("tracker %s\n", version)
	fmt.Printf("  commit: %s\n", commit)
	fmt.Printf("  built:  %s\n", date)
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

	// JSON streaming mode forces non-TUI.
	if cfg.jsonOut {
		cfg.noTUI = true
	}

	// Fall back to plain console mode when TUI is disabled or stdin is not a
	// terminal (e.g. CI, piped input, cron). TUI requires a real TTY.
	if cfg.noTUI || !isatty.IsTerminal(os.Stdin.Fd()) {
		return deps.run(cfg.pipelineFile, cfg.workdir, checkpoint, cfg.format, cfg.verbose, cfg.jsonOut)
	}
	return deps.runTUI(cfg.pipelineFile, cfg.workdir, checkpoint, cfg.format, cfg.verbose)
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
