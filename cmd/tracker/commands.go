// ABOUTME: Command dispatch and shared utilities for the tracker CLI.
// ABOUTME: Routes subcommands, resolves checkpoints, and manages .env loading.
package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/2389-research/dippin-lang/dipx"
	tracker "github.com/2389-research/tracker"
	"github.com/2389-research/tracker/internal/bundleid"
	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/pipeline/handlers"
	"github.com/mattn/go-isatty"
)

func executeCommand(cfg runConfig, deps commandDeps) error {
	deps = fillDefaultDeps(deps)
	return dispatchCommand(cfg, deps)
}

// fillDefaultDeps fills nil function fields in deps with production defaults.
func fillDefaultDeps(deps commandDeps) commandDeps {
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
	return deps
}

// dispatchCommand routes the command mode to the appropriate executor.
func dispatchCommand(cfg runConfig, deps commandDeps) error {
	if cmd, ok := dispatchUtilityCommand(cfg, deps); ok {
		return cmd
	}
	return executeRun(cfg, deps)
}

// dispatchUtilityCommand handles non-run subcommands.
// Returns (result, true) when a subcommand matched, (nil, false) otherwise.
func dispatchUtilityCommand(cfg runConfig, deps commandDeps) (error, bool) {
	if err, ok := dispatchInfoCommands(cfg, deps); ok {
		return err, true
	}
	return dispatchPipelineCommands(cfg)
}

// dispatchInfoCommands handles version/diagnose/doctor/setup/update subcommands.
func dispatchInfoCommands(cfg runConfig, deps commandDeps) (error, bool) {
	switch cfg.mode {
	case modeVersion:
		return executeVersion(), true
	case modeDiagnose:
		return executeDiagnose(cfg), true
	case modeDoctor:
		return executeDoctor(cfg), true
	case modeSetup:
		return deps.runSetup(), true
	case modeUpdate:
		return executeUpdate(), true
	case modeStatus:
		return executeStatus(cfg), true
	case modeRunJSON:
		return executeRunJSON(cfg), true
	}
	return nil, false
}

// dispatchPipelineCommands handles validate/simulate/audit/workflows/init subcommands.
func dispatchPipelineCommands(cfg runConfig) (error, bool) {
	switch cfg.mode {
	case modeValidate:
		return executeValidate(cfg), true
	case modeSimulate:
		return executeSimulate(cfg), true
	case modeEstimate:
		return executeEstimate(cfg), true
	case modeAudit:
		return executeAudit(cfg), true
	case modeWorkflows:
		return executeWorkflows(), true
	case modeInit:
		return executeInit(cfg), true
	case modeVerifyTests:
		return executeVerifyTests(cfg), true
	}
	return nil, false
}

func executeVersion() error {
	// Load env so provider status reflects .env files.
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve working directory: %w", err)
	}
	if err := loadEnvFiles(wd); err != nil {
		return err
	}

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
	if err := loadEnvFiles(cfg.workdir); err != nil {
		return err
	}
	doctorCfg := DoctorConfig{
		probe:        cfg.probe,
		pipelineFile: cfg.pipelineFile,
		backend:      cfg.backend,
		git:          cfg.git,
		allowInit:    cfg.allowInit,
	}
	return runDoctorWithConfig(cfg.workdir, doctorCfg)
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
	var ready []string
	for _, p := range providers {
		if providerHasKey(p.envs) {
			ready = append(ready, p.name)
		}
	}
	if len(ready) > 0 {
		fmt.Printf("  providers: %s\n", strings.Join(ready, ", "))
	} else {
		fmt.Println("  providers: none (run `tracker setup`)")
	}
}

// providerHasKey returns true if any of the given env vars is non-empty.
func providerHasKey(envs []string) bool {
	for _, e := range envs {
		if os.Getenv(e) != "" {
			return true
		}
	}
	return false
}

func executeWorkflows() error {
	workflows := listBuiltinWorkflows()
	if len(workflows) == 0 {
		fmt.Println("No built-in workflows available.")
		return nil
	}

	fmt.Println("\nBuilt-in workflows:")
	fmt.Println()
	fmt.Printf("  %-35s  %-12s  %s\n", "NAME", "REQUIRES", "DESCRIPTION")
	fmt.Printf("  %-35s  %-12s  %s\n", "────", "────────", "───────────")
	for _, wf := range workflows {
		goal := wf.Goal
		if len(goal) > 70 {
			goal = goal[:67] + "..."
		}
		req := strings.Join(wf.Requires, ", ")
		if req == "" {
			req = "—"
		}
		fmt.Printf("  %-35s  %-12s  %s\n", wf.Name+" ("+wf.DisplayName+")", req, goal)
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
		return printInitUsage()
	}

	info, ok := lookupBuiltinWorkflow(cfg.pipelineFile)
	if !ok {
		return buildUnknownWorkflowError(cfg.pipelineFile)
	}

	outFile := info.Name + ".dip"
	if _, err := os.Stat(outFile); err == nil {
		return fmt.Errorf("%s already exists — remove it first or edit it directly", outFile)
	}

	data, _, err := tracker.OpenWorkflow(info.Name)
	if err != nil {
		return fmt.Errorf("read embedded workflow: %w", err)
	}

	if err := os.WriteFile(outFile, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outFile, err)
	}
	fmt.Printf("Created %s\n", outFile)

	// Scaffold a starter SPEC.md for workflows that require one, so the newcomer
	// path (init → edit → run) succeeds instead of hard-exiting on a missing spec
	// (#456). Never overwrites an existing spec.
	fileExists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	writeFile := func(p string, b []byte) error { return os.WriteFile(p, b, 0o644) }
	spec, err := scaffoldStarterSpec(info.Name, fileExists, writeFile)
	if err != nil {
		return fmt.Errorf("write starter %s: %w", starterSpecFile, err)
	}
	if spec != "" {
		fmt.Printf("Created %s — a starter spec; edit it to describe what you want built.\n", spec)
	}

	fmt.Printf("Next: edit the files above, then run: tracker %s\n", info.Name)
	return nil
}

// printInitUsage prints the usage and lists available workflows, then returns an error.
func printInitUsage() error {
	workflows := listBuiltinWorkflows()
	fmt.Fprintf(os.Stderr, "Usage: tracker init <workflow_name>\n\nAvailable workflows:\n")
	for _, wf := range workflows {
		fmt.Fprintf(os.Stderr, "  %s\n", wf.Name)
	}
	return fmt.Errorf("workflow name required")
}

// buildUnknownWorkflowError returns an error listing available built-in workflow names.
func buildUnknownWorkflowError(name string) error {
	workflows := listBuiltinWorkflows()
	var names []string
	for _, wf := range workflows {
		names = append(names, wf.Name)
	}
	return fmt.Errorf("unknown workflow %q (available: %s)", name, strings.Join(names, ", "))
}

func executeValidate(cfg runConfig) error {
	if cfg.pipelineFile == "" {
		return fmt.Errorf("usage: tracker validate <pipeline.dip|pipeline.dot|bundle.dipx>")
	}
	return runValidateCmd(cfg.pipelineFile, cfg.format, os.Stdout)
}

func executeSimulate(cfg runConfig) error {
	if cfg.pipelineFile == "" {
		return fmt.Errorf("usage: tracker simulate <pipeline.dip|pipeline.dot|bundle.dipx>")
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

	// Apply TRACKER_FAIL_ON_OVERRIDE=1 env var fallback. The flag (when set
	// explicitly) always wins; the env var only flips false→true. Must run
	// before newRunOptions copies cfg.failOnOverride below.
	applyFailOnOverrideEnv(&cfg)

	if err := printRunPreamble(cfg); err != nil {
		return err
	}

	printStartupBanner()

	resume, err := resolveRunCheckpoint(cfg)
	if err != nil {
		return err
	}

	// Build the explicit per-invocation config once, from the parsed flags plus
	// the resolved resume metadata, and thread it into run/runTUI. This replaces
	// the former active* package globals; the forced-mismatch detail on resume
	// rides opts.resume through to the activity-log handler (constructed inside
	// run/runTUI) that records the bundle_mismatch_forced entry.
	opts := newRunOptions(cfg, resume)

	return selectAndRunMode(cfg, deps, opts)
}

// printRunPreamble prints backend and autopilot status messages and validates persona.
func printRunPreamble(cfg runConfig) error {
	if cfg.backend != "" && cfg.backend != "native" {
		fmt.Fprintf(os.Stderr, "Agent backend: %s\n", cfg.backend)
	}
	if cfg.webhookURL != "" {
		fmt.Fprintf(os.Stderr, "Webhook gate mode active — human gates will be POSTed to configured URL\n")
		fmt.Fprintf(os.Stderr, "Callback server will start on %s\n", cfg.gateCallbackAddr)
	} else if cfg.autopilot != "" {
		if _, err := handlers.ParsePersona(cfg.autopilot); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Running in autopilot mode (persona: %s) — human gates answered by LLM\n", cfg.autopilot)
	} else if cfg.autoApprove {
		fmt.Fprintln(os.Stderr, "Running in auto-approve mode — all human gates auto-approved")
	}
	printToolSafetyPreamble(cfg)
	return nil
}

// printToolSafetyPreamble surfaces tool-command safety overrides to stderr so
// operators can't accidentally forget they asked the denylist to be bypassed.
// The denylist warning is deliberately loud — it's a security escape hatch.
func printToolSafetyPreamble(cfg runConfig) {
	if cfg.bypassDenylist {
		fmt.Fprintln(os.Stderr, "WARNING: --bypass-denylist active — built-in tool_command denylist is DISABLED")
		fmt.Fprintln(os.Stderr, "         Only run this in sandboxed or trusted environments.")
	}
	if len(cfg.toolAllowlist) > 0 {
		fmt.Fprintf(os.Stderr, "Tool command allowlist active (%d pattern(s)): %s\n",
			len(cfg.toolAllowlist), strings.Join(cfg.toolAllowlist, ", "))
	}
	if len(cfg.toolDenylistAdd) > 0 {
		// --bypass-denylist disables both built-in and user-added patterns,
		// so call that out explicitly — "active" would be misleading when
		// nothing is actually being enforced.
		state := "active"
		if cfg.bypassDenylist {
			state = "configured but bypassed (--bypass-denylist disables these too)"
		}
		fmt.Fprintf(os.Stderr, "Tool command extra denylist %s (%d pattern(s)): %s\n",
			state, len(cfg.toolDenylistAdd), strings.Join(cfg.toolDenylistAdd, ", "))
	}
	if cfg.maxOutputLimit > 0 {
		fmt.Fprintf(os.Stderr, "Tool command output ceiling: %d bytes per stream\n", cfg.maxOutputLimit)
	}
}

// resumeInfo carries the resolved checkpoint path plus any bundle-identity
// override detail that must be threaded through to the activity log once it
// has been constructed (see run/runTUI). The zero value represents a new
// (non-resume) run.
type resumeInfo struct {
	// CheckpointPath is the absolute path to the checkpoint.json for the
	// resumed run, or empty for new runs.
	CheckpointPath string

	// RunID is the run identifier whose checkpoint was loaded. Threaded
	// through so the activity log handler can open the correct
	// <artifactDir>/<runID>/activity.jsonl when emitting the
	// bundle_mismatch_forced event before the engine fires.
	RunID string

	// BundleMismatchForced is true when verifyResumeBundle would have
	// rejected the resume on identity grounds but cfg.forceBundleMismatch
	// allowed it through. Drives the bundle_mismatch_forced audit entry.
	BundleMismatchForced bool

	// OriginalIdentity is the .dipx bundle identity stored in the
	// checkpoint at run-start ("sha256:<hex>" or "" for plain .dip).
	OriginalIdentity string

	// CurrentIdentity is the .dipx bundle identity of the pipeline source
	// the resume is being run against ("sha256:<hex>" or "" for plain .dip).
	CurrentIdentity string
}

// resolveRunCheckpoint returns resume metadata (checkpoint path + bundle
// identity detail) for a resume run. Returns a zero-valued resumeInfo for
// new (non-resume) runs.
//
// For resumes, it also verifies the checkpoint's stored bundle identity
// against the current pipeline source. Any mismatch (including .dipx-to-.dip
// downgrades and .dip-to-.dipx upgrades) aborts the resume unless the user
// explicitly passes --force-bundle-mismatch. When --force-bundle-mismatch
// allows a mismatch through, the returned resumeInfo carries the original
// and current identities so the caller can record the override in the
// activity log via JSONLEventHandler.WriteBundleMismatchForced once that
// handler has been constructed.
func resolveRunCheckpoint(cfg runConfig) (resumeInfo, error) {
	if cfg.resumeID == "" {
		return resumeInfo{}, nil
	}
	cpPath, err := resolveCheckpoint(cfg.workdir, cfg.resumeID)
	if err != nil {
		return resumeInfo{}, err
	}

	cp, err := pipeline.LoadCheckpoint(cpPath)
	if err != nil {
		return resumeInfo{}, fmt.Errorf("load checkpoint for bundle verification: %w", err)
	}

	currentIdentity, err := currentBundleIdentity(cfg.pipelineFile)
	if err != nil {
		return resumeInfo{}, err
	}

	if err := verifyResumeBundle(cp.BundleIdentity, currentIdentity, cfg.forceBundleMismatch); err != nil {
		return resumeInfo{}, err
	}

	info := resumeInfo{
		CheckpointPath:   cpPath,
		RunID:            cp.RunID,
		OriginalIdentity: cp.BundleIdentity,
		CurrentIdentity:  currentIdentity,
	}

	if cp.BundleIdentity != currentIdentity && cfg.forceBundleMismatch {
		info.BundleMismatchForced = true
		fmt.Fprintf(os.Stderr, "WARNING: bundle identity mismatch forced via --force-bundle-mismatch\n  original: %s\n  current:  %s\n",
			bundleid.DisplayForLog(cp.BundleIdentity), bundleid.DisplayForLog(currentIdentity))
	}

	return info, nil
}

// currentBundleIdentity returns the content-addressed identity of the current
// pipeline source, or "" for plain .dip files. Used by resolveRunCheckpoint
// to compare against the checkpoint's stored identity on resume.
func currentBundleIdentity(pipelineFile string) (string, error) {
	if !strings.EqualFold(filepath.Ext(pipelineFile), ".dipx") {
		return "", nil
	}
	bundle, err := dipx.Open(context.Background(), pipelineFile)
	if err != nil {
		return "", fmt.Errorf("resume verification: open bundle %s: %w", pipelineFile, err)
	}
	id := bundle.Identity()
	return "sha256:" + hex.EncodeToString(id[:]), nil
}

// selectAndRunMode picks TUI or plain console mode and starts the pipeline.
func selectAndRunMode(cfg runConfig, deps commandDeps, opts *runOptions) error {
	// JSON streaming forces non-TUI (structured output to stdout).
	if cfg.jsonOut {
		cfg.noTUI = true
	}
	// Fall back to plain console mode when TUI is disabled or stdin is not a
	// terminal (e.g. CI, piped input, cron). TUI requires a real TTY.
	if cfg.noTUI || !isatty.IsTerminal(os.Stdin.Fd()) {
		return deps.run(opts)
	}
	return deps.runTUI(opts)
}

// resolveCheckpoint finds the checkpoint file for a given run ID. It looks in
// .tracker/runs/<runID>/checkpoint.json under the working directory. If the ID
// is a prefix that uniquely matches one run, it resolves to that run.
func resolveCheckpoint(workdir, runID string) (string, error) {
	if runID == "" {
		return "", fmt.Errorf("run ID cannot be empty")
	}
	runsDir := filepath.Join(workdir, ".tracker", "runs")
	resolved, err := resolveRunIDToDir(runsDir, runID)
	if err != nil {
		return "", err
	}
	cpPath := filepath.Join(runsDir, resolved, "checkpoint.json")
	if _, err := os.Stat(cpPath); err != nil {
		return "", fmt.Errorf("checkpoint not found for run %s: %w", resolved, err)
	}
	return cpPath, nil
}

// resolveRunIDToDir finds the unique run directory name for a given run ID or prefix.
func resolveRunIDToDir(runsDir, runID string) (string, error) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return "", fmt.Errorf("cannot read runs directory: %w", err)
	}

	var matches []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), runID) {
			matches = append(matches, e.Name())
		}
	}

	return resolveRunMatches(runsDir, runID, matches)
}

// resolveRunMatches picks the correct directory from a list of prefix matches.
func resolveRunMatches(runsDir, runID string, matches []string) (string, error) {
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no run found matching %q in %s", runID, runsDir)
	case 1:
		return matches[0], nil
	default:
		for _, m := range matches {
			if m == runID {
				return m, nil
			}
		}
		return "", fmt.Errorf("ambiguous run ID %q matches %d runs: %s", runID, len(matches), strings.Join(matches, ", "))
	}
}
