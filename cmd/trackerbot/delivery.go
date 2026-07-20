// ABOUTME: Posts a run's outcome to its thread — diagnosis on failure, adaptive on success (D3).
package main

import (
	"context"
	"fmt"
	"strings"

	tracker "github.com/2389-research/tracker"
	"github.com/2389-research/tracker/pipeline"
)

// deliver posts a finished run's outcome to its thread. On failure it pulls a
// real diagnosis; on success it adapts to what the workflow produced.
func deliver(ctx context.Context, ui ThreadUI, run *tracker.ManagedRun) {
	res, runErr := run.Result()
	if res == nil {
		_ = ui.Post("❌ the run could not start: " + errText(runErr))
		return
	}
	if pipeline.TerminalStatus(res.Status).IsSuccess() {
		deliverSuccess(ui, res)
		return
	}
	deliverFailure(ctx, ui, run.WorkDir, res)
}

// deliverSuccess posts the deliverable.
//
// DECISION POINT (D3) — adapt to WHAT was built. A workflow advertises its
// deliverable by writing a context key; extend these branches as your workflows
// do (a deploy URL, a pushed repo, uploaded files, …).
func deliverSuccess(ui ThreadUI, res *tracker.Result) {
	cost := 0.0
	if res.Cost != nil {
		cost = res.Cost.TotalUSD
	}
	if url := firstNonEmpty(res.Context["deploy_url"], res.Context["url"]); url != "" {
		_ = ui.Post(fmt.Sprintf("🚀 done — %s  ($%.2f)", url, cost))
		return
	}
	if summary := res.Context["delivery"]; summary != "" {
		_ = ui.Post(fmt.Sprintf("🏁 %s  ($%.2f)", summary, cost))
		return
	}
	_ = ui.Post(fmt.Sprintf("🏁 done — *%s* ($%.2f).\nArtifacts: `%s`", res.Status, cost, res.ArtifactRunDir))
}

// deliverFailure posts a diagnosis of the failed run, or a terse fallback.
func deliverFailure(ctx context.Context, ui ThreadUI, workDir string, res *tracker.Result) {
	if msg, ok := diagnose(ctx, workDir, res.RunID); ok {
		_ = ui.Post(msg)
		return
	}
	_ = ui.Post(fmt.Sprintf("❌ the run finished *%s* — I couldn't pull a diagnosis.", res.Status))
}

// diagnose reads the run dir and formats the failing nodes + suggestions.
func diagnose(ctx context.Context, workDir, runID string) (string, bool) {
	if workDir == "" || runID == "" {
		return "", false
	}
	runDir, err := tracker.ResolveRunDir(workDir, runID)
	if err != nil {
		return "", false
	}
	rep, err := tracker.Diagnose(ctx, runDir)
	if err != nil || (len(rep.Failures) == 0 && len(rep.Suggestions) == 0) {
		return "", false
	}
	return formatDiagnosis(rep), true
}

// formatDiagnosis renders a DiagnoseReport into a thread message.
func formatDiagnosis(rep *tracker.DiagnoseReport) string {
	var b strings.Builder
	b.WriteString("❌ the run failed.\n")
	for _, f := range rep.Failures {
		fmt.Fprintf(&b, "• *%s* (%s)", f.NodeID, f.Outcome)
		if e := firstError(f); e != "" {
			fmt.Fprintf(&b, " — %s", truncate(e, 300))
		}
		b.WriteByte('\n')
	}
	for _, s := range rep.Suggestions {
		fmt.Fprintf(&b, "💡 %s\n", s.Message)
	}
	return b.String()
}

func firstError(f tracker.NodeFailure) string {
	if len(f.Errors) > 0 {
		return f.Errors[0]
	}
	return strings.TrimSpace(f.Stderr)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func errText(err error) string {
	if err == nil {
		return "unknown error"
	}
	return err.Error()
}
