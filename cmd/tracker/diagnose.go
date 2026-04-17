// ABOUTME: Diagnose subcommand — deep analysis of pipeline run failures.
// ABOUTME: Reads activity.jsonl and node status files to surface errors, tool output, and suggestions.
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	tracker "github.com/2389-research/tracker"
	"github.com/charmbracelet/lipgloss"
)

// diagnoseMostRecent finds and diagnoses the most recent run.
func diagnoseMostRecent(workdir string) error {
	report, err := tracker.DiagnoseMostRecent(workdir)
	if err != nil {
		return err
	}
	printDiagnoseReport(report)
	return nil
}

// runDiagnose performs deep failure analysis on a pipeline run.
func runDiagnose(workdir, runID string) error {
	runDir, err := tracker.ResolveRunDir(workdir, runID)
	if err != nil {
		return err
	}
	report, err := tracker.Diagnose(runDir)
	if err != nil {
		return err
	}
	printDiagnoseReport(report)
	return nil
}

// printDiagnoseReport is the top-level entry point that composes all print helpers.
func printDiagnoseReport(r *tracker.DiagnoseReport) {
	printDiagnoseHeader(r)
}

// printDiagnoseHeader renders the diagnose banner, budget halt section (if any),
// and node failure details.
func printDiagnoseHeader(r *tracker.DiagnoseReport) {
	fmt.Println()
	fmt.Println(bannerStyle.Render("tracker diagnose"))
	fmt.Println()
	fmt.Printf("  Run ID:  %s\n", r.RunID)
	fmt.Printf("  Nodes:   %d completed\n", r.CompletedNodes)

	// Surface budget halt prominently before other sections.
	if r.BudgetHalt != nil {
		printBudgetHalt(r.BudgetHalt)
	}

	if len(r.Failures) == 0 && r.BudgetHalt == nil {
		fmt.Println()
		fmt.Println(lipgloss.NewStyle().Foreground(colorNeon).Render("  No failures found — this run completed cleanly."))
		fmt.Println()
		return
	}

	if len(r.Failures) > 0 {
		printNodeFailures(r.Failures, r.Suggestions)
	}
}

// printNodeFailures prints the failure count, per-node diagnosis, and suggestions.
func printNodeFailures(failures []tracker.NodeFailure, suggestions []tracker.Suggestion) {
	fmt.Printf("  Failures: %d\n", len(failures))
	fmt.Println()

	// Sort failures by node ID for deterministic output.
	sorted := make([]tracker.NodeFailure, len(failures))
	copy(sorted, failures)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].NodeID < sorted[j].NodeID
	})

	for i := range sorted {
		printNodeDiagnosis(&sorted[i])
	}

	// Print suggestions.
	printDiagnoseSuggestions(suggestions)
}

// printBudgetHalt prints a prominent budget halt section.
func printBudgetHalt(halt *tracker.BudgetHalt) {
	w := os.Stdout
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "━━━ Budget halt detected ━━━")
	if halt.Message != "" {
		fmt.Fprintf(w, "  breach:       %s\n", halt.Message)
	}
	if halt.TotalTokens > 0 {
		fmt.Fprintf(w, "  tokens used:  %d\n", halt.TotalTokens)
	}
	if halt.TotalCostUSD > 0 {
		fmt.Fprintf(w, "  cost:         $%.4f\n", halt.TotalCostUSD)
	}
	if halt.WallElapsedMs > 0 {
		fmt.Fprintf(w, "  wall time:    %dms\n", halt.WallElapsedMs)
	}
	fmt.Fprintln(w, "  suggestion:   raise the relevant --max-tokens, --max-cost, or --max-wall-time flag,")
	fmt.Fprintln(w, "                or remove the Config.Budget value in your pipeline configuration")
	fmt.Fprintln(w, "")
}

func printNodeDiagnosis(f *tracker.NodeFailure) {
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(colorHot)
	labelStyle := lipgloss.NewStyle().Foreground(colorSky).Bold(true)

	fmt.Println(headerStyle.Render(fmt.Sprintf("  ✗ %s", f.NodeID)))

	printNodeDiagnosisMeta(f, labelStyle)
	printIndentedBlock(labelStyle, "Output:", f.Stdout)
	printIndentedBlock(labelStyle, "Stderr:", f.Stderr)
	printNodeDiagnosisErrors(f, labelStyle)

	// If no useful info was found, say so.
	if f.Stdout == "" && f.Stderr == "" && len(f.Errors) == 0 {
		fmt.Printf("    %s\n", mutedStyle.Render("No error details captured — node may have failed silently"))
	}

	fmt.Println()
}

// printNodeDiagnosisMeta prints handler, duration, and retry count for a node failure.
func printNodeDiagnosisMeta(f *tracker.NodeFailure, labelStyle lipgloss.Style) {
	if f.Handler != "" {
		fmt.Printf("    %s %s\n", labelStyle.Render("Handler:"), f.Handler)
	}
	if f.Duration > 0 {
		durationLabel := "Duration:"
		if f.RetryCount >= 2 {
			durationLabel = "Duration (last):"
		}
		fmt.Printf("    %s %s\n", labelStyle.Render(durationLabel), formatElapsed(f.Duration))
	}
	if f.RetryCount >= 2 {
		retryInfo := fmt.Sprintf("%d failures", f.RetryCount)
		if f.IdenticalRetries {
			retryInfo += " (all identical — deterministic)"
		}
		fmt.Printf("    %s %s\n", labelStyle.Render("Attempts:"), retryInfo)
	}
}

// printNodeDiagnosisErrors prints deduplicated error messages for a node failure.
func printNodeDiagnosisErrors(f *tracker.NodeFailure, labelStyle lipgloss.Style) {
	if len(f.Errors) == 0 {
		return
	}
	seen := make(map[string]bool)
	fmt.Printf("    %s\n", labelStyle.Render("Errors:"))
	for _, e := range f.Errors {
		if !seen[e] {
			seen[e] = true
			fmt.Printf("      %s\n", e)
		}
	}
}

// printIndentedBlock prints a labeled multi-line block with 6-space indent.
func printIndentedBlock(labelStyle lipgloss.Style, label, content string) {
	if content == "" {
		return
	}
	fmt.Printf("    %s\n", labelStyle.Render(label))
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			fmt.Printf("      %s\n", line)
		}
	}
}

func printDiagnoseSuggestions(suggestions []tracker.Suggestion) {
	fmt.Println("─── Suggestions ───────────────────────────────────────────")

	if len(suggestions) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, s := range suggestions {
			if s.Kind == tracker.SuggestionBudget {
				continue // printBudgetHalt already shows this
			}
			fmt.Printf("  %s %s\n", lipgloss.NewStyle().Foreground(colorWarm).Render("→"), s.Message)
		}
	}
	fmt.Println()
}
