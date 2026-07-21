// ABOUTME: Posts a run's outcome to its thread — diagnosis on failure, adaptive on success (D3).
package chatops

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	tracker "github.com/2389-research/tracker"
	"github.com/2389-research/tracker/pipeline"
)

// deliver posts a finished run's outcome to its thread. On failure it pulls a
// real diagnosis; on success it adapts to what the workflow produced.
func deliver(ctx context.Context, ui ThreadUI, run *tracker.ManagedRun) {
	res, runErr := run.Result()
	if res == nil {
		// No run dir to diagnose — lead with the classified cause of the
		// start-time failure (config, auth, billing…) instead of a raw wrapper.
		if fc := tracker.ClassifyFailure(runErr); fc.Title != "" {
			_ = ui.Post("❌ the run couldn't start — " + formatCause(fc))
			return
		}
		_ = ui.Post("❌ the run could not start: " + errText(runErr))
		return
	}
	if pipeline.TerminalStatus(res.Status).IsSuccess() {
		deliverSuccess(ui, res)
		return
	}
	deliverFailure(ctx, ui, run.WorkDir, res, runErr)
}

// formatCause renders a FailureCause as a compact thread message: the title, the
// cause, and the next step.
func formatCause(fc tracker.FailureCause) string {
	var b strings.Builder
	b.WriteString("*" + strings.TrimSpace(fc.Icon+" "+fc.Title) + "*")
	if fc.Detail != "" {
		b.WriteString("\n" + fc.Detail)
	}
	if fc.NextSteps != "" {
		b.WriteString("\n" + fc.NextSteps)
	}
	return b.String()
}

// deliverSuccess posts the results card: outcome, cost, duration, and the
// deliverable itself — a link if the run produced one, else the artifacts.
func deliverSuccess(ui ThreadUI, res *tracker.Result) {
	cost := 0.0
	if res.Cost != nil {
		cost = res.Cost.TotalUSD
	}
	var b strings.Builder
	fmt.Fprintf(&b, "✅ done — *%s* · $%.2f", res.Status, cost)
	if d := runDuration(res); d > 0 {
		fmt.Fprintf(&b, " · %s", shortDur(d))
	}
	b.WriteByte('\n')

	switch d := detectDeliverable(res); {
	case d.URL != "":
		fmt.Fprintf(&b, "🔗 %s", d.URL)
	case d.Summary != "":
		b.WriteString(d.Summary)
	default:
		fmt.Fprintf(&b, "📦 Artifacts: `%s`", res.ArtifactRunDir)
	}
	b.WriteString("\n_Mention me again to iterate._")
	_ = ui.Post(b.String())
}

// Deliverable describes what a successful run produced, for presentation.
type Deliverable struct {
	URL     string // a deploy/PR/preview URL surfaced from the run's output
	Summary string // a workflow-provided delivery summary (ctx["delivery"])
}

var urlRe = regexp.MustCompile(`https?://[^\s>)"']+`)

// detectDeliverable inspects a run's output for something worth handing back: an
// explicit deploy/PR URL, any URL found in the context, or a delivery summary.
// This is the "land the plane" adaptation (D3) — extend the explicit keys as
// your workflows advertise deliverables.
func detectDeliverable(res *tracker.Result) Deliverable {
	d := Deliverable{Summary: res.Context["delivery"]}
	d.URL = firstNonEmpty(
		res.Context["deploy_url"], res.Context["pr_url"],
		res.Context["preview_url"], res.Context["url"],
	)
	if d.URL == "" {
		d.URL = scanForURL(res.Context)
	}
	return d
}

// scanForURL returns the first http(s) URL across context values, scanning keys
// in sorted order for deterministic results.
func scanForURL(ctx map[string]string) string {
	keys := make([]string, 0, len(ctx))
	for k := range ctx {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if m := urlRe.FindString(ctx[k]); m != "" {
			return m
		}
	}
	return ""
}

func runDuration(res *tracker.Result) time.Duration {
	if res.Trace == nil || res.Trace.StartTime.IsZero() || res.Trace.EndTime.IsZero() {
		return 0
	}
	return res.Trace.EndTime.Sub(res.Trace.StartTime)
}

func shortDur(d time.Duration) string {
	d = d.Round(time.Second)
	if m := int(d / time.Minute); m > 0 {
		return fmt.Sprintf("%dm%02ds", m, int((d%time.Minute)/time.Second))
	}
	return fmt.Sprintf("%ds", int(d/time.Second))
}

// deliverFailure posts a diagnosis of the failed run, or a terse fallback, with
// a one-word recovery nudge. A budget breach gets a `bump` nudge (raise the
// ceiling and re-run) instead of a plain `retry` — re-running at the same cap
// would just breach again.
func deliverFailure(ctx context.Context, ui ThreadUI, workDir string, res *tracker.Result, runErr error) {
	hint := recoveryHint(res)
	if msg, ok := diagnose(ctx, workDir, res.RunID); ok {
		_ = ui.Post(msg + hint)
		return
	}
	// No diagnosis available — lead with the classified cause when we have one,
	// instead of a bare "couldn't pull a diagnosis" (#492).
	if fc := tracker.ClassifyFailure(runErr); fc.Kind != "" && fc.Kind != "generic" {
		_ = ui.Post("❌ " + formatCause(fc) + hint)
		return
	}
	_ = ui.Post(fmt.Sprintf("❌ the run finished *%s* — I couldn't pull a diagnosis.%s", res.Status, hint))
}

// recoveryHint picks the one-line recovery nudge for a failed run: a budget bump
// when the run hit its cost ceiling, a plain retry otherwise.
func recoveryHint(res *tracker.Result) string {
	switch pipeline.TerminalStatus(res.Status) {
	case pipeline.OutcomeBudgetExceeded:
		next := suggestBumpDollars(res)
		return fmt.Sprintf("\n💸 Hit the cost ceiling. Reply `bump %d` to re-run with a $%d ceiling.", next, next)
	case pipeline.OutcomePausedBilling:
		// Paused, not failed — retrying now hits the same empty balance. Add
		// credit first. (`retry` re-runs from the start in-thread; a checkpoint
		// resume from the paused node is available on the CLI via `tracker -r`.)
		return "\n⏸ Paused — add credit to the flagged account, then reply `retry` to run it again."
	default:
		return "\n_Reply `retry` to run it again._"
	}
}

// suggestBumpDollars proposes a raised ceiling: roughly double what the run
// already spent, rounded up to whole dollars, floored at $2 so a tiny run still
// gets meaningful headroom.
func suggestBumpDollars(res *tracker.Result) int {
	spent := 0.0
	if res.Cost != nil {
		spent = res.Cost.TotalUSD
	}
	next := int(spent*2) + 1
	if next < 2 {
		next = 2
	}
	return next
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
