// ABOUTME: Preflight health check — verifies API keys, dippin binary, workdir, and more.
// ABOUTME: Surfaces actionable guidance for common setup issues.
// ABOUTME: Exit 0 = all pass, Exit 1 = any failure, Exit 2 = warnings only (no errors).
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tracker "github.com/2389-research/tracker"
	"github.com/charmbracelet/lipgloss"
)

// DoctorWarningsError is returned by runDoctorWithConfig when there are
// warnings but no hard failures. main.go maps this to os.Exit(2).
type DoctorWarningsError struct {
	Warnings int
}

func (e *DoctorWarningsError) Error() string {
	return fmt.Sprintf("doctor: %d warning(s) (no failures)", e.Warnings)
}

// DoctorConfig is the CLI's internal options struct for `tracker doctor`.
// It is distinct from tracker.DoctorConfig: these field names are the
// CLI's historical lowercase convention, and only the CLI parses flags
// into this type.
type DoctorConfig struct {
	probe        bool
	pipelineFile string
	backend      string
}

// DoctorResult retains counts exposed to older tests. The authoritative
// tally lives on tracker.DoctorReport (Warnings/Errors + derived Passed).
type DoctorResult struct {
	Passed   int
	Warnings int
	Failures int
}

// formatLLMClientError massages the "no providers configured" error into
// an actionable setup hint. Used by run.go when building the LLM client.
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

func runDoctor(workdir string) error {
	return runDoctorWithConfig(workdir, DoctorConfig{})
}

// runDoctorWithConfig runs preflight checks (via the tracker library) and
// prints the results using the CLI's glamour format. Any write-side-effect
// fix-ups (e.g. patching .gitignore) happen here, not in the library.
func runDoctorWithConfig(workdir string, cfg DoctorConfig) error {
	if workdir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("cannot determine working directory: %w", err)
		}
		workdir = wd
	}

	report, err := tracker.Doctor(tracker.DoctorConfig{
		WorkDir:        workdir,
		Backend:        cfg.backend,
		ProbeProviders: cfg.probe,
		PipelineFile:   cfg.pipelineFile,
		TrackerVersion: version,
		TrackerCommit:  commit,
	})
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Println(bannerStyle.Render("tracker doctor"))
	fmt.Println()

	for _, c := range report.Checks {
		fmt.Printf("  %s\n", c.Name)
		printCheckResult(c)
		fmt.Println()
	}

	// Write-side-effect fix-ups must not run inside the library. The
	// Working Directory check emits a gitignore hint in its details;
	// patch the file here now that the user has seen the warning.
	maybeFixGitignore(report, workdir)

	result := toDoctorResult(report)
	printSummary(result)
	fmt.Println()
	fmt.Println(mutedStyle.Render("  exit codes: 0=all pass  1=failures  2=warnings only"))
	fmt.Println()

	if result.Failures > 0 {
		return fmt.Errorf("health check failed")
	}
	if result.Warnings > 0 {
		return &DoctorWarningsError{Warnings: result.Warnings}
	}
	return nil
}

// printCheckResult renders one CheckResult in the historical format. For
// checks with per-item details, each detail is printed; the composite
// line is only shown for checks that the CLI treats as single-fact
// (Environment Warnings, Dippin Language, Disk Space).
func printCheckResult(c tracker.CheckResult) {
	for _, d := range c.Details {
		switch d.Status {
		case "ok":
			printCheck(true, d.Message)
		case "warn":
			printWarn(d.Message)
		case "hint":
			printHint(d.Message)
		default:
			printCheck(false, d.Message)
		}
		if d.Hint != "" {
			printHint(d.Hint)
		}
	}
	if !needsCompositeResultLine(c.Name) {
		// Composite line already implied by per-item details above.
		if c.Hint != "" && c.Status != "ok" {
			printHint(c.Hint)
		}
		return
	}
	switch c.Status {
	case "ok":
		printCheck(true, c.Message)
	case "warn":
		printWarn(c.Message)
	default:
		printCheck(false, c.Message)
	}
	if c.Hint != "" && c.Status != "ok" {
		printHint(c.Hint)
	}
}

// needsCompositeResultLine identifies checks whose per-item details already
// cover every line the user sees. For those, printCheckResult skips the
// composite summary to avoid a duplicate bullet.
func needsCompositeResultLine(checkName string) bool {
	switch checkName {
	case "LLM Providers", "Version Compatibility", "Optional Binaries",
		"Artifact Directories", "Working Directory", "Pipeline File":
		return false
	}
	return true
}

// toDoctorResult recomputes Passed/Warnings/Failures in the CLI's legacy
// counting scheme: a check with Status=warn OR (status=error AND the CLI
// considers it non-required) becomes a Warning. tracker.DoctorReport has
// already flattened this, so we simply map Warnings and Errors straight
// through, deriving Passed by subtraction.
func toDoctorResult(report *tracker.DoctorReport) DoctorResult {
	total := len(report.Checks)
	return DoctorResult{
		Passed:   total - report.Warnings - report.Errors,
		Warnings: report.Warnings,
		Failures: report.Errors,
	}
}

// maybeFixGitignore patches .gitignore when the library flagged it as
// missing entries. The library detected the issue but did not write —
// gitignore mutation is a CLI-only concern so tests and embedded callers
// do not unexpectedly touch user files.
func maybeFixGitignore(report *tracker.DoctorReport, workdir string) {
	for _, c := range report.Checks {
		if c.Name != "Working Directory" {
			continue
		}
		for _, d := range c.Details {
			if strings.HasPrefix(d.Message, ".gitignore not found") ||
				strings.HasPrefix(d.Message, ".gitignore missing entries") {
				checkGitignore(workdir)
				return
			}
		}
	}
}

// checkGitignore detects missing entries and appends them to the file.
// This is the only write side effect in the doctor command; kept in the
// CLI so the library stays read-only.
func checkGitignore(workdir string) {
	gitignorePath := filepath.Join(workdir, ".gitignore")
	content, err := os.ReadFile(gitignorePath)
	if err != nil {
		// File absent — the library already emitted a warn detail line.
		return
	}
	entries := make(map[string]bool)
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entries[strings.TrimRight(line, "/")] = true
	}
	want := []struct {
		bare    string
		display string
	}{
		{".tracker", ".tracker/"},
		{"runs", "runs/"},
		{".ai", ".ai/"},
	}
	var toAppend []string
	for _, w := range want {
		if !entries[w.bare] {
			toAppend = append(toAppend, w.display)
		}
	}
	if len(toAppend) == 0 {
		return
	}
	// Append missing entries (best-effort; silent on error — the user
	// already saw the warning).
	suffix := "\n"
	if len(content) > 0 && content[len(content)-1] != '\n' {
		suffix = "\n"
	} else {
		suffix = ""
	}
	suffix += strings.Join(toAppend, "\n") + "\n"
	_ = os.WriteFile(gitignorePath, append(content, []byte(suffix)...), 0o644)
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
