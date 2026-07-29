// ABOUTME: Terminal-status classification and recommendation helpers for Audit.
// ABOUTME: Maps activity + checkpoint into a Status string, a stable StatusClass, and recs.
package tracker

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

// statusClassFor maps a Status string to its stable bucket class. The buckets
// are "succeeded", "failed", and "paused" — the last for the recoverable,
// resumable paused_billing terminal (#514), which a control plane must not read
// as dead. Everything else routes through pipeline.TerminalStatus.IsSuccess().
// Centralized so AuditReport and RunSummary stay in lockstep.
func statusClassFor(status string) string {
	if pipeline.TerminalStatus(status) == pipeline.OutcomePausedBilling {
		return "paused"
	}
	if pipeline.TerminalStatus(status).IsSuccess() {
		return "succeeded"
	}
	return "failed"
}

// classifyStatus collapses a run's activity log and checkpoint into a single
// status string for the audit/list surfaces. Algorithm per spec §6.4:
//
//  1. Reverse-scan activity entries. pipeline_failed / budget_exceeded /
//     billing_paused short-circuit (a terminal halt dominates; billing_paused
//     resolves to the resumable paused_billing status, #514). pipeline_completed
//     and validation_overridden are observed but the scan continues so a later
//     (i.e. earlier-in-scan) terminal event can still override them.
//  2. If both pipeline_completed and validation_overridden were observed in
//     the scan, return "validation_overridden". A lone pipeline_completed
//     resolves to "success".
//  3. If no terminal activity event (completed/failed/budget) was observed,
//     fall back to checkpoint signals: a non-empty CurrentNode means the run
//     halted mid-graph → "fail"; a sticky ValidationOverrides on a finished
//     run (CurrentNode == "") → "validation_overridden"; otherwise "success".
//
// D12 fix (Gap 5.2): budget_exceeded no longer collapses to "fail" — it
// surfaces as its own status string. Scripts that previously filtered on
// status == "fail" will see budget-halted runs leave that bucket.
func classifyStatus(cp *pipeline.Checkpoint, activity []ActivityEntry) string {
	if s, ok := statusFromActivity(activity); ok {
		return s
	}
	return statusFromCheckpoint(cp)
}

// statusFromActivity reverse-scans activity for a terminal or observed status.
// It returns (status, true) when the activity log is authoritative and
// ("", false) when the caller must fall back to checkpoint signals.
func statusFromActivity(activity []ActivityEntry) (string, bool) {
	sawCompletion := false
	sawOverride := false
	for i := len(activity) - 1; i >= 0; i-- {
		if s, ok := terminalHaltStatus(activity[i].Type); ok {
			return s, true
		}
		switch activity[i].Type {
		case "pipeline_completed":
			sawCompletion = true
		case "validation_overridden":
			sawOverride = true
		}
	}
	if sawCompletion {
		if sawOverride {
			return "validation_overridden", true
		}
		return "success", true
	}
	return "", false
}

// terminalHaltStatus maps a single activity event type to the terminal status
// it forces (failure or a recoverable pause), if any. billing_paused resolves
// to the resumable paused_billing status (#487/#514) so a paused run is never
// misreported as failed via the checkpoint CurrentNode fallback.
func terminalHaltStatus(eventType string) (string, bool) {
	switch eventType {
	case "pipeline_failed":
		return "fail", true
	case "budget_exceeded":
		return "budget_exceeded", true
	case "billing_paused":
		return string(pipeline.OutcomePausedBilling), true
	}
	return "", false
}

// statusFromCheckpoint resolves a run's status from checkpoint signals when the
// activity log carried no terminal event. A lone validation_overridden event in
// the log is not treated as terminal here; it only contributes when paired with
// pipeline_completed in statusFromActivity.
func statusFromCheckpoint(cp *pipeline.Checkpoint) string {
	if len(cp.ValidationOverrides) > 0 && cp.CurrentNode == "" {
		return "validation_overridden"
	}
	if cp.CurrentNode != "" {
		return "fail"
	}
	return "success"
}

// buildAuditRecommendations assembles the recommendation list for an AuditReport.
//
// Entries are emitted in priority order — override notes first (a one-line
// summary + one per-override chronological entry), then per-node retry
// suggestions (sorted by node ID for stable test output), then restart and
// long-running notes, then a halted-at hint for fail / budget_exceeded runs.
// No sort.Strings: the order matters and downstream consumers that want
// alphabetical can sort on receipt.
func buildAuditRecommendations(cp *pipeline.Checkpoint, status string, total time.Duration, overrides []pipeline.OverrideDetail) []string {
	var recs []string
	recs = append(recs, overrideRecs(overrides)...)
	recs = append(recs, retryRecs(cp)...)
	recs = append(recs, restartRecs(cp)...)
	if total > 30*time.Minute {
		recs = append(recs, "Long-running pipeline — consider fidelity=summary:medium for faster resumes")
	}
	recs = append(recs, haltedRecs(cp, status)...)
	// No sort.Strings(recs) — entries appear in priority order per D16:
	// override notes → retry → restart → long-running → halted-at.
	return recs
}

// overrideRecs builds the D16 override-notes block: a summary line plus one
// entry per OverrideDetail in the (chronological) order supplied by the caller.
func overrideRecs(overrides []pipeline.OverrideDetail) []string {
	if len(overrides) == 0 {
		return nil
	}
	recs := []string{
		"This run terminated via a validation override. Workflow completion does not imply spec compliance — the override path bypassed at least one automated gate.",
	}
	for _, d := range overrides {
		gate := d.GateNodeID
		if len(d.SubgraphPath) > 0 {
			parts := make([]string, 0, len(d.SubgraphPath)+1)
			parts = append(parts, d.SubgraphPath...)
			parts = append(parts, d.GateNodeID)
			gate = strings.Join(parts, "/")
		}
		recs = append(recs,
			fmt.Sprintf("Validation override at gate %q (label: %q, actor: %s). Review the override decision to confirm it meets project policy.",
				gate, d.Label, d.Actor))
	}
	return recs
}

// retryRecs builds per-node retry suggestions, iterating the retry map in
// node-ID order so the emitted entries are deterministic for tests/snapshots —
// map range is random in Go and would otherwise produce flaky ordering.
func retryRecs(cp *pipeline.Checkpoint) []string {
	if len(cp.RetryCounts) == 0 {
		return nil
	}
	ids := make([]string, 0, len(cp.RetryCounts))
	for id := range cp.RetryCounts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var recs []string
	for _, id := range ids {
		if count := cp.RetryCounts[id]; count >= 2 {
			recs = append(recs, fmt.Sprintf("Consider adjusting retry_policy for %s (used %d retries)", id, count))
		}
	}
	return recs
}

// restartRecs builds the restart note (empty when the run never restarted).
func restartRecs(cp *pipeline.Checkpoint) []string {
	if cp.RestartCount <= 0 {
		return nil
	}
	suffix := "time"
	if cp.RestartCount > 1 {
		suffix = "times"
	}
	return []string{fmt.Sprintf("Pipeline restarted %d %s — review loop conditions", cp.RestartCount, suffix)}
}

// haltedRecs surfaces a "halted at" hint for both fail and budget_exceeded.
// Pre-Gap-5.2 budget-halted runs were classified as "fail" so this branch
// caught them; after D12 they surface as their own status string and would
// otherwise silently no-op here.
func haltedRecs(cp *pipeline.Checkpoint, status string) []string {
	if (status != "fail" && status != "budget_exceeded") || cp.CurrentNode == "" {
		return nil
	}
	verb := "failed"
	if status == "budget_exceeded" {
		verb = "halted (budget exceeded)"
	}
	return []string{fmt.Sprintf("Pipeline %s at %s — check error details above", verb, cp.CurrentNode)}
}
