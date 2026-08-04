// ABOUTME: Diagnose subcommand — deep analysis of pipeline run failures.
// ABOUTME: Reads activity.jsonl and node status files to surface errors, tool output, and suggestions.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tracker "github.com/2389-research/tracker"
	"github.com/2389-research/tracker/pipeline"
	"github.com/charmbracelet/lipgloss"
)

// diagnoseMostRecent finds and diagnoses the most recent run.
func diagnoseMostRecent(workdir string) error {
	report, err := tracker.DiagnoseMostRecent(context.Background(), workdir, tracker.DiagnoseConfig{LogWriter: io.Discard})
	if err != nil {
		return err
	}
	// Resolve the run dir from the report's ID so the unpriced signal (#518),
	// which lives in run.json, is surfaced on the most-recent path too.
	runDir, _ := tracker.ResolveRunDir(workdir, report.RunID)
	printDiagnoseReport(report, readUnpricedModels(runDir))
	return nil
}

// runDiagnose performs deep failure analysis on a pipeline run.
func runDiagnose(workdir, runID string) error {
	runDir, err := tracker.ResolveRunDir(workdir, runID)
	if err != nil {
		return err
	}
	report, err := tracker.Diagnose(context.Background(), runDir, tracker.DiagnoseConfig{LogWriter: io.Discard})
	if err != nil {
		return err
	}
	printDiagnoseReport(report, readUnpricedModels(runDir))
	return nil
}

// printDiagnoseReport is the top-level entry point that composes all print
// helpers. unpriced names any uncatalogued models the run billed against (#518);
// nil when the run priced cleanly or run.json is absent.
func printDiagnoseReport(r *tracker.DiagnoseReport, unpriced []string) {
	printDiagnoseHeader(r, unpriced)
}

// printDiagnoseHeader renders the diagnose banner, budget halt section (if any),
// and node failure details.
func printDiagnoseHeader(r *tracker.DiagnoseReport, unpriced []string) {
	fmt.Println()
	fmt.Println(bannerStyle.Render("tracker diagnose"))
	fmt.Println()
	fmt.Printf("  Run ID:  %s\n", r.RunID)
	fmt.Printf("  Nodes:   %d completed\n", r.CompletedNodes)

	// Surface budget halt prominently before other sections.
	if r.BudgetHalt != nil {
		printBudgetHalt(r.BudgetHalt)
	}

	// Surface validation overrides between BudgetHalt and Failures (spec §9.4).
	// Informational only — override is NOT a failure, so this section may be
	// rendered alongside (or instead of) the failure section, and the
	// clean-run early-return below treats "overrides without failures" as a
	// non-empty report.
	if len(r.ValidationOverrides) > 0 {
		printValidationOverrides(r.ValidationOverrides)
	}

	// Unpriced usage (#518) is informational like overrides: a clean run can
	// still have billed against an uncatalogued model, so surface it before the
	// clean-run early-return and count it as something-to-report.
	if len(unpriced) > 0 {
		printUnpricedUsage(unpriced)
	}

	if diagnoseHasNothingToReport(r, unpriced) {
		fmt.Println()
		fmt.Println(lipgloss.NewStyle().Foreground(colorNeon).Render("  No failures found — this run completed cleanly."))
		fmt.Println()
		return
	}

	if len(r.Failures) > 0 {
		printNodeFailures(r.Failures, r.Suggestions)
	}
}

// diagnoseHasNothingToReport is true when there is no failure, budget halt,
// override, or unpriced signal — the only case where the clean-run message is
// the whole report.
func diagnoseHasNothingToReport(r *tracker.DiagnoseReport, unpriced []string) bool {
	return len(r.Failures) == 0 && r.BudgetHalt == nil &&
		len(r.ValidationOverrides) == 0 && len(unpriced) == 0
}

// readUnpricedModels reads run.json from runDir and returns the uncatalogued
// model names it billed against, or nil when the run priced cleanly, the
// manifest is absent, or runDir is empty. Best-effort: any read/parse error is
// treated as "nothing to surface" — diagnose must never fail on telemetry.
func readUnpricedModels(runDir string) []string {
	if runDir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(runDir, pipeline.RunManifestFile)) //nolint:gosec // composed from a resolved run dir
	if err != nil {
		return nil
	}
	var m pipeline.RunManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	if !m.Totals.Unpriced {
		return nil
	}
	if len(m.Totals.UnpricedModels) > 0 {
		return m.Totals.UnpricedModels
	}
	return []string{"an uncatalogued model"}
}

// printUnpricedUsage renders the informational "Unpriced Usage" section (#518):
// models with no catalog entry were billed at $0, so any --max-cost ceiling
// could not bound them.
func printUnpricedUsage(models []string) {
	fmt.Println()
	fmt.Println("─── Unpriced Usage ─────")
	fmt.Printf("  Models:   %s\n", strings.Join(models, ", "))
	fmt.Println("  No catalog entry, so cost was estimated as $0 and a --max-cost")
	fmt.Println("  ceiling could not bound this usage. Add these models to the")
	fmt.Println("  catalog to price the run.")
	fmt.Println()
}

// printValidationOverrides renders the informational "Validation Override"
// section. Per spec §9.4 this is NOT a failure surface — it is a forensic
// trail showing which gates were overridden, by whom, and on which edge
// label. Sub-graph paths are rendered as outer/inner/.../gate when present.
func printValidationOverrides(overrides []pipeline.OverrideDetail) {
	fmt.Println()
	fmt.Println("─── Validation Override ─────")
	for _, d := range overrides {
		gate := d.GateNodeID
		if len(d.SubgraphPath) > 0 {
			gate = strings.Join(append(append([]string{}, d.SubgraphPath...), d.GateNodeID), "/")
		}
		fmt.Printf("  Gate:     %s\n", gate)
		fmt.Printf("  Label:    %q\n", d.Label)
		fmt.Printf("  Actor:    %s\n", d.Actor)
		fmt.Println()
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
