// ABOUTME: Preflight health check — verifies API keys, dippin binary, workdir, and more.
// ABOUTME: Surfaces actionable guidance for common setup issues.
// ABOUTME: Exit 0 = all pass, Exit 1 = any failure, Exit 2 = warnings only (no errors).
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/llm/anthropic"
	"github.com/2389-research/tracker/llm/google"
	"github.com/2389-research/tracker/llm/openai"
	"github.com/2389-research/tracker/pipeline"
	"github.com/charmbracelet/lipgloss"
)

// checkResult holds the outcome of a single doctor check.
type checkResult struct {
	ok      bool
	warn    bool   // true = warning (not a hard failure)
	message string // human-readable status
	fix     string // actionable fix on failure, empty on pass
}

// check is a named doctor check.
type check struct {
	name     string
	run      func() checkResult
	required bool // if true, failure increments Failures; otherwise Warnings
}

// DoctorConfig holds doctor command configuration.
type DoctorConfig struct {
	probe        bool   // perform live auth validation (network call per provider)
	pipelineFile string // optional pipeline file to validate
	backend      string // if "claude-code", check for claude CLI
}

// DoctorResult tracks check outcomes.
type DoctorResult struct {
	Passed   int
	Warnings int
	Failures int
}

// formatLLMClientError wraps LLM client creation errors with actionable hints.
func formatLLMClientError(err error) error {
	if strings.Contains(err.Error(), "no providers configured") {
		return fmt.Errorf(`no LLM providers configured

  Set at least one API key:
    export ANTHROPIC_API_KEY=sk-ant-...
    export OPENAI_API_KEY=sk-...
    export GEMINI_API_KEY=...

  Or run: tracker setup`)
	}
	return fmt.Errorf("create LLM client: %w", err)
}

// runDoctor performs a preflight health check and prints results.
func runDoctor(workdir string) error {
	cfg := DoctorConfig{}
	return runDoctorWithConfig(workdir, cfg)
}

// runDoctorWithConfig performs a preflight health check with optional config.
func runDoctorWithConfig(workdir string, cfg DoctorConfig) error {
	if workdir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("cannot determine working directory: %w", err)
		}
		workdir = wd
	}

	fmt.Println()
	fmt.Println(bannerStyle.Render("tracker doctor"))
	fmt.Println()

	checks := buildChecks(workdir, cfg)
	result := DoctorResult{}

	for _, c := range checks {
		fmt.Printf("  %s\n", c.name)
		cr := c.run()

		// Some checks print per-item lines internally; needsCompositeResultLine
		// returns false for those. For simple checks, print the summary line here.
		if needsCompositeResultLine(c.name) {
			switch {
			case cr.ok && !cr.warn:
				printCheck(true, cr.message)
			case cr.warn:
				printWarn(cr.message)
			default:
				printCheck(false, cr.message)
			}
		}
		if cr.fix != "" && !cr.ok {
			printHint(cr.fix)
		}

		switch {
		case cr.ok && !cr.warn:
			result.Passed++
		case cr.warn || (!cr.ok && !c.required):
			result.Warnings++
		default:
			result.Failures++
		}
		fmt.Println()
	}

	printSummary(result)
	fmt.Println()
	fmt.Println(mutedStyle.Render("  exit codes: 0=all pass  1=failures  2=warnings only"))
	fmt.Println()

	if result.Failures > 0 {
		return fmt.Errorf("health check failed")
	}
	return nil
}

// needsCompositeResultLine returns true for checks that rely on the outer loop
// to print their result line. Checks that print multiple internal lines handle
// their own output and return false.
func needsCompositeResultLine(checkName string) bool {
	switch checkName {
	case "LLM Providers", "Version Compatibility", "Optional Binaries",
		"Artifact Directories", "Working Directory", "Pipeline File":
		return false
	}
	return true
}

// buildChecks returns the ordered list of checks to run.
func buildChecks(workdir string, cfg DoctorConfig) []check {
	checks := []check{
		{
			name:     "Environment Warnings",
			run:      checkEnvWarnings,
			required: false,
		},
		{
			name:     "LLM Providers",
			run:      func() checkResult { return checkProviders(cfg.probe) },
			required: true,
		},
		{
			name:     "Dippin Language",
			run:      checkDippin,
			required: true,
		},
		{
			name:     "Version Compatibility",
			run:      checkVersionCompat,
			required: false,
		},
		{
			name:     "Optional Binaries",
			run:      func() checkResult { return checkOtherBinaries(cfg.backend) },
			required: false,
		},
		{
			name:     "Working Directory",
			run:      func() checkResult { return checkWorkdir(workdir) },
			required: true,
		},
		{
			name:     "Artifact Directories",
			run:      func() checkResult { return checkArtifactDirs(workdir) },
			required: false,
		},
		{
			name:     "Disk Space",
			run:      func() checkResult { return checkDiskSpace(workdir) },
			required: false,
		},
	}

	if cfg.pipelineFile != "" {
		pf := cfg.pipelineFile
		checks = append(checks, check{
			name:     "Pipeline File",
			run:      func() checkResult { return checkPipelineFile(pf) },
			required: true,
		})
	}

	return checks
}

// ---- Individual check functions --------------------------------------------

// checkEnvWarnings flags dangerous environment variables.
func checkEnvWarnings() checkResult {
	dangerousVars := map[string]string{
		"TRACKER_PASS_ENV":      "passes all env vars to tool subprocesses (security risk)",
		"TRACKER_PASS_API_KEYS": "passes API keys to tool subprocesses (security risk)",
	}

	var found []string
	for envVar, desc := range dangerousVars {
		if os.Getenv(envVar) != "" {
			found = append(found, fmt.Sprintf("%s (%s)", envVar, desc))
		}
	}

	if len(found) == 0 {
		return checkResult{ok: true, message: "no dangerous environment variables detected"}
	}
	return checkResult{
		ok:      false,
		warn:    true,
		message: fmt.Sprintf("dangerous variables set: %s", strings.Join(found, "; ")),
		fix:     "unset TRACKER_PASS_ENV and TRACKER_PASS_API_KEYS to restore default security posture",
	}
}

// providerDef describes a single LLM provider for doctor checks.
type providerDef struct {
	name         string
	envVars      []string
	defaultModel string
	// buildAdapter constructs a provider adapter from a key for live --probe.
	// nil means the probe is skipped for this provider.
	buildAdapter func(key string) (llm.ProviderAdapter, error)
}

// knownProviders is the ordered list of LLM provider definitions.
var knownProviders = []providerDef{
	{
		name:         "Anthropic",
		envVars:      []string{"ANTHROPIC_API_KEY"},
		defaultModel: "claude-haiku-3-5",
		buildAdapter: func(key string) (llm.ProviderAdapter, error) { return anthropic.New(key), nil },
	},
	{
		name:         "OpenAI",
		envVars:      []string{"OPENAI_API_KEY"},
		defaultModel: "gpt-4o-mini",
		buildAdapter: func(key string) (llm.ProviderAdapter, error) { return openai.New(key), nil },
	},
	{
		name:         "OpenAI-Compat",
		envVars:      []string{"OPENAI_COMPAT_API_KEY"},
		defaultModel: "gpt-4o-mini",
		buildAdapter: nil, // requires base URL; skip live probe
	},
	{
		name:         "Gemini",
		envVars:      []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"},
		defaultModel: "gemini-2.0-flash",
		buildAdapter: func(key string) (llm.ProviderAdapter, error) { return google.New(key), nil },
	},
}

// checkProviders checks all configured LLM providers.
// If probe=true, performs a live auth ping for each configured provider.
func checkProviders(probe bool) checkResult {
	foundAny := false

	for _, p := range knownProviders {
		key, envName := findProviderKey(p.envVars)
		if key == "" {
			printCheck(false, fmt.Sprintf("%-15s %s not set", p.name, p.envVars[0]))
			continue
		}

		masked := maskKey(key)
		if !isValidAPIKey(p.name, key) {
			printCheck(false, fmt.Sprintf("%-15s %s=%s (invalid format)", p.name, envName, masked))
			printHint(fmt.Sprintf("%s keys should match expected format — run `tracker setup`", p.name))
			continue
		}

		if probe && p.buildAdapter != nil {
			authOk, authMsg := probeProvider(p, key)
			if !authOk {
				printCheck(false, fmt.Sprintf("%-15s %s=%s (auth failed: %s)", p.name, envName, masked, authMsg))
				printHint(fmt.Sprintf("your %s key is invalid or expired — export a fresh key or run `tracker setup`", p.name))
				continue
			}
			printCheck(true, fmt.Sprintf("%-15s %s=%s (auth verified)", p.name, envName, masked))
		} else {
			printCheck(true, fmt.Sprintf("%-15s %s=%s", p.name, envName, masked))
		}
		foundAny = true
	}

	if !foundAny {
		return checkResult{
			ok:      false,
			message: "no LLM providers configured",
			fix:     "run `tracker setup` or export ANTHROPIC_API_KEY / OPENAI_API_KEY / GEMINI_API_KEY",
		}
	}
	if probe {
		return checkResult{ok: true, message: "provider(s) configured and auth verified"}
	}
	return checkResult{ok: true, message: "provider(s) configured"}
}

// findProviderKey scans envVars and returns the first non-empty key and its env name.
func findProviderKey(envVars []string) (key, envName string) {
	for _, e := range envVars {
		if v := os.Getenv(e); v != "" {
			return v, e
		}
	}
	return "", ""
}

// probeProvider performs a minimal live auth check by sending a 1-token completion.
// Returns (true, "") on success, (false, reason) on auth/network failure.
func probeProvider(p providerDef, key string) (bool, string) {
	adapter, err := p.buildAdapter(key)
	if err != nil {
		return false, fmt.Sprintf("build adapter: %v", err)
	}

	client, err := llm.NewClient(llm.WithProvider(adapter))
	if err != nil {
		return false, fmt.Sprintf("create client: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	maxTok := 1
	req := &llm.Request{
		Model:     p.defaultModel,
		Messages:  []llm.Message{llm.UserMessage("ping")},
		MaxTokens: &maxTok,
	}

	_, err = client.Complete(ctx, req)
	if err != nil {
		msg := err.Error()
		if isAuthError(msg) {
			return false, "invalid or expired API key"
		}
		return false, trimErrMsg(msg, 80)
	}
	return true, ""
}

// isAuthError returns true when the error string indicates an authentication failure.
func isAuthError(msg string) bool {
	lower := strings.ToLower(msg)
	for _, kw := range []string{"401", "403", "unauthorized", "authentication", "invalid api key", "api key", "forbidden"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// trimErrMsg trims an error message to maxLen characters.
func trimErrMsg(msg string, maxLen int) string {
	if len(msg) <= maxLen {
		return msg
	}
	return msg[:maxLen] + "..."
}

// checkDippin verifies the dippin binary is in PATH and reports its version.
func checkDippin() checkResult {
	path, err := exec.LookPath("dippin")
	if err != nil {
		return checkResult{
			ok:      false,
			message: "dippin binary not found in PATH",
			fix:     "install from https://github.com/2389-research/dippin-lang  (required for pipeline linting)",
		}
	}

	ver := getDippinVersion(path)
	printCheck(true, fmt.Sprintf("dippin %s at %s", ver, path))
	return checkResult{ok: true, message: fmt.Sprintf("dippin %s", ver)}
}

// getDippinVersion returns the version string of the dippin binary.
func getDippinVersion(path string) string {
	out, err := exec.Command(path, "--version").CombinedOutput()
	if err != nil {
		out, err = exec.Command(path, "version").CombinedOutput()
		if err != nil {
			return "(version unknown)"
		}
	}
	ver := strings.TrimSpace(string(out))
	ver = strings.TrimPrefix(ver, "dippin ")
	ver = strings.TrimPrefix(ver, "version ")
	if ver == "" {
		return "(version unknown)"
	}
	return ver
}

// checkVersionCompat reports tracker and dippin versions side-by-side.
func checkVersionCompat() checkResult {
	printCheck(true, fmt.Sprintf("tracker   %s (commit %s)", version, commit))

	dippinPath, err := exec.LookPath("dippin")
	if err != nil {
		printWarn("dippin not found — skipping version compatibility check")
		return checkResult{
			ok:      false,
			warn:    true,
			message: fmt.Sprintf("tracker %s / dippin not found", version),
		}
	}

	cliVer := getDippinVersion(dippinPath)
	printCheck(true, fmt.Sprintf("dippin    %s", cliVer))
	return checkResult{ok: true, message: fmt.Sprintf("tracker %s / dippin %s", version, cliVer)}
}

// checkOtherBinaries checks for optional but useful binaries.
func checkOtherBinaries(backend string) checkResult {
	allOk := true

	if _, err := exec.LookPath("git"); err == nil {
		printCheck(true, "git found (recommended for pipeline versioning)")
	} else {
		printWarn("git not found in PATH (recommended for pipeline versioning)")
		allOk = false
	}

	claudePath, claudeErr := exec.LookPath("claude")
	if claudeErr == nil {
		claudeVer := getBinaryVersion(claudePath, "--version")
		printCheck(true, fmt.Sprintf("claude %s (for --backend claude-code)", claudeVer))
	} else if backend == "claude-code" {
		printCheck(false, "claude CLI not found in PATH (required for --backend claude-code)")
		printHint("install the Claude CLI from https://claude.ai/code")
		allOk = false
	} else {
		printWarn("claude not found in PATH (install for --backend claude-code support)")
	}

	if allOk {
		return checkResult{ok: true, message: "optional binaries available"}
	}
	return checkResult{
		ok:      false,
		warn:    true,
		message: "some optional binaries missing",
		fix:     "install git and/or the Claude CLI to unlock all tracker features",
	}
}

// getBinaryVersion runs <path> <flag> and returns the trimmed first line of output.
func getBinaryVersion(path, flag string) string {
	out, err := exec.Command(path, flag).CombinedOutput()
	if err != nil {
		return "(version unknown)"
	}
	lines := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)
	if len(lines) == 0 {
		return "(version unknown)"
	}
	return strings.TrimSpace(lines[0])
}

// checkWorkdir verifies the working directory exists and is writable.
func checkWorkdir(workdir string) checkResult {
	info, err := os.Stat(workdir)
	if err != nil {
		return checkResult{
			ok:      false,
			message: fmt.Sprintf("%s does not exist", workdir),
			fix:     fmt.Sprintf("create the directory: mkdir -p %s", workdir),
		}
	}
	if !info.IsDir() {
		return checkResult{
			ok:      false,
			message: fmt.Sprintf("%s is not a directory", workdir),
			fix:     "point --workdir at a directory, not a file",
		}
	}

	testFile := filepath.Join(workdir, ".tracker_test_write")
	if err := os.WriteFile(testFile, []byte("test"), 0600); err != nil {
		return checkResult{
			ok:      false,
			message: fmt.Sprintf("%s is not writable", workdir),
			fix:     fmt.Sprintf("check permissions: chmod u+w %s", workdir),
		}
	}
	os.Remove(testFile)

	home, _ := os.UserHomeDir()
	if workdir == home || workdir == "/" {
		printWarn(fmt.Sprintf("%s (risk of accidental data loss — use a project subdirectory)", workdir))
	}

	checkGitignore(workdir)

	printCheck(true, fmt.Sprintf("%s (writable)", workdir))
	return checkResult{ok: true, message: fmt.Sprintf("%s is writable", workdir)}
}

// checkGitignore warns if .gitignore lacks .tracker/ and runs/ entries.
func checkGitignore(workdir string) {
	gitignorePath := filepath.Join(workdir, ".gitignore")
	content, err := os.ReadFile(gitignorePath)
	if err != nil {
		printWarn(".gitignore not found — add .tracker/ and runs/ to prevent committing run artifacts")
		return
	}
	cs := string(content)
	var missing []string
	if !strings.Contains(cs, ".tracker") {
		missing = append(missing, ".tracker/")
	}
	if !strings.Contains(cs, "runs") {
		missing = append(missing, "runs/")
	}
	if len(missing) > 0 {
		printWarn(fmt.Sprintf(".gitignore missing entries: %s", strings.Join(missing, ", ")))
	}
}

// checkArtifactDirs verifies artifact storage directories are (or can be) writable.
func checkArtifactDirs(workdir string) checkResult {
	allOk := true

	// Check TRACKER_ARTIFACT_DIR if configured.
	artifactDir := os.Getenv("TRACKER_ARTIFACT_DIR")
	if artifactDir != "" {
		if !checkDirWritable(artifactDir, "TRACKER_ARTIFACT_DIR") {
			allOk = false
		}
	}

	// Check .ai/ subdir (used by many built-in workflows).
	aiDir := filepath.Join(workdir, ".ai")
	if info, err := os.Stat(aiDir); err == nil {
		if !info.IsDir() {
			printCheck(false, ".ai is not a directory")
			allOk = false
		} else if !isDirWritable(aiDir) {
			printCheck(false, fmt.Sprintf("%s exists but is not writable", aiDir))
			printHint(fmt.Sprintf("check permissions: chmod u+w %s", aiDir))
			allOk = false
		} else {
			printCheck(true, fmt.Sprintf("%s exists and is writable", aiDir))
		}
	} else {
		// .ai/ does not exist — check parent writability so it can be created.
		if isDirWritable(workdir) {
			printCheck(true, fmt.Sprintf("%s will be created on first run", aiDir))
		} else {
			printCheck(false, fmt.Sprintf("%s cannot be created (parent not writable)", aiDir))
			allOk = false
		}
	}

	if allOk {
		return checkResult{ok: true, message: "artifact directories writable"}
	}
	return checkResult{
		ok:      false,
		warn:    true,
		message: "some artifact directories have permission issues",
		fix:     "fix directory permissions or update TRACKER_ARTIFACT_DIR",
	}
}

// checkDirWritable checks if a directory exists and is writable, printing inline results.
func checkDirWritable(dir, label string) bool {
	info, err := os.Stat(dir)
	if err != nil {
		printCheck(false, fmt.Sprintf("%s=%s does not exist", label, dir))
		printHint(fmt.Sprintf("create the directory: mkdir -p %s", dir))
		return false
	}
	if !info.IsDir() {
		printCheck(false, fmt.Sprintf("%s=%s is not a directory", label, dir))
		return false
	}
	if !isDirWritable(dir) {
		printCheck(false, fmt.Sprintf("%s=%s is not writable", label, dir))
		printHint(fmt.Sprintf("fix permissions: chmod u+w %s", dir))
		return false
	}
	printCheck(true, fmt.Sprintf("%s=%s (writable)", label, dir))
	return true
}

// isDirWritable returns true if dir is writable by the current user.
func isDirWritable(dir string) bool {
	probe := filepath.Join(dir, ".tracker_write_probe")
	if err := os.WriteFile(probe, []byte("x"), 0600); err != nil {
		return false
	}
	os.Remove(probe)
	return true
}

// checkDiskSpace checks free space in the working directory.
func checkDiskSpace(workdir string) checkResult {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(workdir, &stat); err != nil {
		return checkResult{
			ok:      false,
			warn:    true,
			message: fmt.Sprintf("could not determine disk space: %v", err),
		}
	}

	available := stat.Bavail * uint64(stat.Bsize)
	availableGB := float64(available) / (1024 * 1024 * 1024)
	const minGB = 1.0

	if availableGB < minGB {
		return checkResult{
			ok:      false,
			warn:    true,
			message: fmt.Sprintf("low disk space: %.2f GB available (recommended: %.1f GB+)", availableGB, minGB),
			fix:     "free up disk space before running long pipelines",
		}
	}
	return checkResult{ok: true, message: fmt.Sprintf("%.2f GB available", availableGB)}
}

// checkPipelineFile validates a pipeline file using the same path as `tracker validate`.
func checkPipelineFile(pipelineFile string) checkResult {
	if _, err := os.Stat(pipelineFile); err != nil {
		return checkResult{
			ok:      false,
			message: fmt.Sprintf("%s does not exist", pipelineFile),
			fix:     fmt.Sprintf("check the file path: %s", pipelineFile),
		}
	}

	if !strings.HasSuffix(pipelineFile, ".dip") {
		printWarn(fmt.Sprintf("%s is not a .dip file — may not be a valid pipeline", pipelineFile))
	}

	format := ""
	if strings.HasSuffix(pipelineFile, ".dip") {
		format = "dip"
	} else if strings.HasSuffix(pipelineFile, ".dot") {
		format = "dot"
	}

	graph, err := loadPipeline(pipelineFile, format)
	if err != nil {
		return checkResult{
			ok:      false,
			message: fmt.Sprintf("%s: parse error: %v", pipelineFile, err),
			fix:     "run `tracker validate " + pipelineFile + "` for full details",
		}
	}

	registry := buildValidationRegistry()
	ve := pipeline.ValidateAllWithLint(graph, registry)

	if ve != nil && len(ve.Errors) > 0 {
		for _, e := range ve.Errors {
			printCheck(false, fmt.Sprintf("error: %s", e))
		}
		for _, w := range ve.Warnings {
			printWarn(w)
		}
		return checkResult{
			ok:      false,
			message: fmt.Sprintf("%s failed validation (%d error(s))", pipelineFile, len(ve.Errors)),
			fix:     "run `tracker validate " + pipelineFile + "` for full details",
		}
	}

	if ve != nil && len(ve.Warnings) > 0 {
		for _, w := range ve.Warnings {
			printWarn(w)
		}
		printCheck(true, fmt.Sprintf("%s valid (%d nodes, %d edges, %d warning(s))",
			pipelineFile, len(graph.Nodes), len(graph.Edges), len(ve.Warnings)))
		return checkResult{
			ok:      true,
			warn:    true,
			message: fmt.Sprintf("%s valid with %d warning(s)", pipelineFile, len(ve.Warnings)),
		}
	}

	printCheck(true, fmt.Sprintf("%s valid (%d nodes, %d edges)", pipelineFile, len(graph.Nodes), len(graph.Edges)))
	return checkResult{ok: true, message: fmt.Sprintf("%s is valid", pipelineFile)}
}

// ---- Rendering helpers -----------------------------------------------------

func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

// isValidAPIKey checks if a key matches the expected format for a provider.
func isValidAPIKey(provider string, key string) bool {
	if key == "" {
		return false
	}
	switch provider {
	case "Anthropic":
		return strings.HasPrefix(key, "sk-ant-") && len(key) > 10
	case "OpenAI", "OpenAI-Compat":
		return strings.HasPrefix(key, "sk-") && len(key) > 10
	case "Gemini":
		return len(key) > 10
	}
	return len(key) > 5
}

func printCheck(ok bool, msg string) {
	if ok {
		fmt.Printf("    %s %s\n", lipgloss.NewStyle().Foreground(colorNeon).Render("✓"), msg)
	} else {
		fmt.Printf("    %s %s\n", lipgloss.NewStyle().Foreground(colorHot).Render("✗"), msg)
	}
}

func printWarn(msg string) {
	fmt.Printf("    %s %s\n", lipgloss.NewStyle().Foreground(lipgloss.Color("#FFAA00")).Render("⚠"), msg)
}

func printHint(msg string) {
	fmt.Printf("    %s\n", mutedStyle.Render("→ "+msg))
}

// printSummary prints a summary of the check results.
func printSummary(result DoctorResult) {
	summary := fmt.Sprintf("  %d passed", result.Passed)
	if result.Warnings > 0 {
		summary += fmt.Sprintf(", %d warning", result.Warnings)
		if result.Warnings > 1 {
			summary += "s"
		}
	}
	if result.Failures > 0 {
		summary += fmt.Sprintf(", %d failure", result.Failures)
		if result.Failures > 1 {
			summary += "s"
		}
	}

	if result.Failures == 0 {
		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(colorNeon).Render(summary))
	} else {
		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(colorHot).Render(summary))
	}
}
