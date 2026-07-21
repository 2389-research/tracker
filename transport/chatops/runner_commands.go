// ABOUTME: Runner control verbs (help/status/cancel/runs/retry/workflows) and the
// ABOUTME: live-status registry that lets `status` report a run's in-flight progress.
package chatops

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tracker "github.com/2389-research/tracker"
)

const helpText = "*trackerbot* — run Tracker pipelines from Slack.\n" +
	"• `@trackerbot <what you want>` — start a run (I'll pick a workflow), or `run <workflow> [k=v …]`\n" +
	"• `@trackerbot retry` — re-run this thread's last workflow\n" +
	"• `@trackerbot bump <dollars>` — re-run with a raised cost ceiling\n" +
	"• `@trackerbot steer <guidance>` — nudge the running workflow with a note\n" +
	"• `@trackerbot workflows` — list workflows you can run\n" +
	"• `@trackerbot status` — this thread's run state\n" +
	"• `@trackerbot cancel` — stop this thread's run\n" +
	"• `@trackerbot runs` — list active runs\n" +
	"• `@trackerbot help` — this message"

// handleCommand handles control verbs (help/status/cancel/runs/retry). Returns
// true if the mention was a command and no fresh run should be started.
func (r *Runner) handleCommand(ctx context.Context, ui ThreadUI, threadTS, text string) bool {
	cmd := strings.TrimSpace(stripMention(text))
	// `bump <dollars>` and `steer <text>` carry an argument, so match their
	// prefixes (in handleArgCommand) before the exact-match verbs below.
	if r.handleArgCommand(ctx, ui, threadTS, cmd) {
		return true
	}
	switch strings.ToLower(cmd) {
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

// handleArgCommand dispatches the verbs that take an argument (`bump`, `steer`),
// matched by prefix. Returns true if cmd was one of them.
func (r *Runner) handleArgCommand(ctx context.Context, ui ThreadUI, threadTS, cmd string) bool {
	if arg, ok := commandArg(cmd, "bump"); ok {
		r.bumpBudget(ctx, ui, threadTS, arg)
		return true
	}
	if arg, ok := commandArg(cmd, "steer"); ok {
		r.steerRun(ui, threadTS, arg)
		return true
	}
	return false
}

// commandArg reports whether cmd is the given verb followed by an argument
// ("bump 10" → "10", true), matching the verb case-insensitively. A bare verb
// with no argument returns ("", true) so the handler can show usage.
func commandArg(cmd, verb string) (arg string, ok bool) {
	fields := strings.Fields(cmd)
	if len(fields) == 0 || !strings.EqualFold(fields[0], verb) {
		return "", false
	}
	return strings.TrimSpace(strings.Join(fields[1:], " ")), true
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
	r.launch(ctx, ui, source, rec, fmt.Sprintf("🔁 re-running `%s`.", info.DisplayName), 0)
}

// bumpBudget re-runs the thread's last workflow with a raised cost ceiling — the
// one-word recovery from a budget_exceeded terminal. It's a fresh run (the prior
// run's checkpoint was reaped on terminal), just with more headroom. arg is the
// new ceiling in whole dollars ("bump 10" → $10).
func (r *Runner) bumpBudget(ctx context.Context, ui ThreadUI, threadTS, arg string) {
	dollars, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(arg, "$")), 64)
	if err != nil || dollars <= 0 {
		_ = ui.Post("Usage: `bump <dollars>` — e.g. `bump 10` to re-run with a $10 ceiling.")
		return
	}
	rec, ok := r.recall(threadTS)
	if !ok {
		_ = ui.Post("Nothing to bump in this thread yet — start a run first.")
		return
	}
	source, info, err := tracker.ResolveSource(rec.Workflow, r.deps.WorkDir)
	if err != nil {
		_ = ui.Post("Couldn't re-run: " + err.Error())
		return
	}
	r.launch(ctx, ui, source, rec,
		fmt.Sprintf("💪 re-running `%s` with a $%.2f ceiling.", info.DisplayName, dollars),
		int(dollars*100))
}

// steerGuidanceKey is the context key mid-run steering notes land under. It is
// in the `steer.*` namespace — never on the tool_command interpolation
// allowlist (per CLAUDE.md), so a note can't be injected into a shell command —
// and a workflow references it like any other context value.
const steerGuidanceKey = "steer.guidance"

// steerRun injects a guidance note into the thread's running workflow via its
// steering channel. The note surfaces at the next inter-node boundary, visible
// to edge selection and the next node's prompt. A workflow only acts on it if it
// references `steer.guidance`; for others it's a harmless no-op context value.
func (r *Runner) steerRun(ui ThreadUI, threadTS, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		_ = ui.Post("Usage: `steer <guidance>` — inject a note the running workflow can act on.")
		return
	}
	r.steerMu.Lock()
	ch := r.steerCh[threadTS]
	r.steerMu.Unlock()
	if ch == nil {
		_ = ui.Post("No active run in this thread to steer.")
		return
	}
	// Non-blocking: the engine drains between nodes, so the buffer is normally
	// empty. A full buffer means many un-drained steers stacked up mid-step —
	// tell the user to retry rather than block the transport goroutine.
	select {
	case ch <- map[string]string{steerGuidanceKey: text}:
		// Honest wording: the note is queued and applied at the next inter-node
		// boundary — a non-blocking send only guarantees it's buffered, not that
		// the engine (which may be mid-node, or finishing) will consume it.
		_ = ui.Post("🧭 steering queued (applies at the next step): " + text)
	default:
		_ = ui.Post("Couldn't steer right now (the run is mid-step) — try again in a moment.")
	}
}

// steerBufSize is the steering channel's buffer — it absorbs bursts between the
// engine's inter-node drains without the sender ever blocking.
const steerBufSize = 8

// registerSteering records a run's steering channel for the thread so `steer`
// can reach it. Called only after a successful rm.Start, so a rejected duplicate
// mention can't overwrite a live run's channel.
func (r *Runner) registerSteering(threadTS string, ch chan map[string]string) {
	r.steerMu.Lock()
	r.steerCh[threadTS] = ch
	r.steerMu.Unlock()
}

// unregisterSteering drops the thread's steering channel on run completion. It
// does NOT close the channel: a `steer` racing completion may already hold the
// channel reference, and a send on a closed channel would panic. Dropping the
// map entry (plus the engine releasing its receive side) lets GC reclaim it; a
// late send lands in the unread buffer and is discarded.
func (r *Runner) unregisterSteering(threadTS string) {
	r.steerMu.Lock()
	delete(r.steerCh, threadTS)
	r.steerMu.Unlock()
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
