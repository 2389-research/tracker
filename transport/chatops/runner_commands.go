// ABOUTME: Runner control verbs (help/status/cancel/runs/retry/workflows) and the
// ABOUTME: live-status registry that lets `status` report a run's in-flight progress.
package chatops

import (
	"context"
	"fmt"
	"strings"

	tracker "github.com/2389-research/tracker"
)

const helpText = "*trackerbot* — run Tracker pipelines from Slack.\n" +
	"• `@trackerbot <what you want>` — start a run (I'll pick a workflow), or `run <workflow> [k=v …]`\n" +
	"• `@trackerbot retry` — re-run this thread's last workflow\n" +
	"• `@trackerbot workflows` — list workflows you can run\n" +
	"• `@trackerbot status` — this thread's run state\n" +
	"• `@trackerbot cancel` — stop this thread's run\n" +
	"• `@trackerbot runs` — list active runs\n" +
	"• `@trackerbot help` — this message"

// handleCommand handles control verbs (help/status/cancel/runs/retry). Returns
// true if the mention was a command and no fresh run should be started.
func (r *Runner) handleCommand(ctx context.Context, ui ThreadUI, threadTS, text string) bool {
	switch strings.ToLower(strings.TrimSpace(stripMention(text))) {
	case "", "help", "?":
		_ = ui.Post(helpText)
	case "status":
		r.postStatus(ui, threadTS)
	case "cancel", "stop":
		r.postCancel(ui, threadTS)
	case "runs", "list":
		r.postRuns(ui)
	case "retry", "again", "rerun":
		r.retryLast(ctx, ui, threadTS)
	case "workflows", "wf":
		r.postWorkflows(ui)
	default:
		return false
	}
	return true
}

// postWorkflows lists the built-in workflows a user can run.
func (r *Runner) postWorkflows(ui ThreadUI) {
	wfs := tracker.Workflows()
	if len(wfs) == 0 {
		_ = ui.Post("No built-in workflows available.")
		return
	}
	var b strings.Builder
	b.WriteString("*Workflows you can run:*\n")
	for _, wf := range wfs {
		if goal := truncate(wf.Goal, 80); goal != "" {
			fmt.Fprintf(&b, "• `%s` — %s\n", wf.Name, goal)
		} else {
			fmt.Fprintf(&b, "• `%s`\n", wf.Name)
		}
	}
	b.WriteString("_Run one: `@trackerbot run <name>`, or just describe what you want._")
	_ = ui.Post(b.String())
}

// suggestWorkflows posts a compact hint of a few workflow names — shown when a
// request couldn't be resolved, so the user isn't left at a dead end.
func (r *Runner) suggestWorkflows(ui ThreadUI) {
	wfs := tracker.Workflows()
	if len(wfs) == 0 {
		return
	}
	names := make([]string, 0, 5)
	for _, wf := range wfs {
		names = append(names, "`"+wf.Name+"`")
		if len(names) == 5 {
			break
		}
	}
	_ = ui.Post("Try one of: " + strings.Join(names, ", ") + " — or `@trackerbot workflows` for the full list.")
}

// retryLast re-runs the thread's most recent workflow (a fresh run, not a resume).
func (r *Runner) retryLast(ctx context.Context, ui ThreadUI, threadTS string) {
	rec, ok := r.recall(threadTS)
	if !ok {
		_ = ui.Post("Nothing to retry in this thread yet — start a run first.")
		return
	}
	source, info, err := tracker.ResolveSource(rec.Workflow, r.deps.WorkDir)
	if err != nil {
		_ = ui.Post("Couldn't re-run: " + err.Error())
		return
	}
	r.launch(ctx, ui, source, rec, fmt.Sprintf("🔁 re-running `%s`.", info.DisplayName))
}

func (r *Runner) remember(threadTS string, rec RunRecord) {
	r.lastMu.Lock()
	r.last[threadTS] = rec
	r.lastMu.Unlock()
}

func (r *Runner) recall(threadTS string) (RunRecord, bool) {
	r.lastMu.Lock()
	defer r.lastMu.Unlock()
	rec, ok := r.last[threadTS]
	return rec, ok
}

func (r *Runner) postStatus(ui ThreadUI, threadTS string) {
	run, ok := r.rm.Get(threadTS)
	if !ok {
		_ = ui.Post("No run in this thread.")
		return
	}
	msg := fmt.Sprintf("Status: *%s*%s", run.State(), runIDSuffix(run.RunID()))
	if line := r.statusProgress(threadTS); line != "" {
		msg += " · " + line
	}
	_ = ui.Post(msg)
}

// statusProgress returns the live "5/9 steps · $1.12 · Implement" digest for a
// thread's run, or "" when the transport has no status card (non-StatusRenderer)
// or nothing has happened yet.
func (r *Runner) statusProgress(threadTS string) string {
	r.stMu.Lock()
	st := r.trackers[threadTS]
	r.stMu.Unlock()
	if st == nil {
		return ""
	}
	return statusLine(st.Card())
}

func (r *Runner) trackStatus(threadTS string, st *statusTracker) {
	r.stMu.Lock()
	r.trackers[threadTS] = st
	r.stMu.Unlock()
}

func (r *Runner) untrackStatus(threadTS string) {
	r.stMu.Lock()
	delete(r.trackers, threadTS)
	r.stMu.Unlock()
}

func (r *Runner) postCancel(ui ThreadUI, threadTS string) {
	if r.rm.Cancel(threadTS) {
		_ = ui.Post("🛑 cancelling this run…")
		return
	}
	_ = ui.Post("No active run in this thread to cancel.")
}

func (r *Runner) postRuns(ui ThreadUI) {
	runs := r.rm.List()
	if len(runs) == 0 {
		_ = ui.Post("No runs right now.")
		return
	}
	var b strings.Builder
	b.WriteString("*Active runs:*\n")
	for _, run := range runs {
		fmt.Fprintf(&b, "• `%s` — %s\n", run.Key, run.State())
	}
	_ = ui.Post(b.String())
}

func runIDSuffix(runID string) string {
	if runID == "" {
		return ""
	}
	return fmt.Sprintf(" (run `%s`)", runID)
}
