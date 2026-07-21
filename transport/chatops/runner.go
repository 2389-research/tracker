// ABOUTME: Runner maps Slack threads to concurrent tracker runs via the RunManager.
// ABOUTME: OnMention starts a run; OnInteraction routes a reply/click to the run's gate.
package chatops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	tracker "github.com/2389-research/tracker"
	"github.com/2389-research/tracker/pipeline"
)

// RunnerDeps are the transport-provided hooks and per-run config a Runner needs.
type RunnerDeps struct {
	// NewThreadUI returns a ThreadUI bound to one (channel, thread) — supplied by
	// the transport (slack.go) so the runner stays Slack-agnostic.
	NewThreadUI func(channel, threadTS string) ThreadUI
	// WorkDir is where ResolveSource looks for local .dip files (built-ins
	// resolve regardless).
	WorkDir string
	// RunsBase is the parent directory for per-thread isolated run workdirs
	// (base/<sanitized thread_ts>), each holding that run's checkpoint.
	RunsBase string
	// NewID returns a fresh unique gate id per call.
	NewID func() string
	// Intent resolves @mention text to a workflow + params. Nil falls back to the
	// deterministic grammar ("[run] <workflow> [k=v ...]").
	Intent IntentResolver
	// Store persists active runs for resume-after-restart. Nil disables it.
	Store *Store
	// KeepWorkdirs retains a run's workdir after it finishes (for later
	// inspection) instead of reclaiming the disk. Default false: reap on
	// terminal, bounding disk under sustained multi-run load.
	KeepWorkdirs bool
	// ConfirmOverUSD requires a human to confirm a run whose expected cost meets
	// or exceeds this dollar amount before it starts. 0 disables confirmation.
	ConfirmOverUSD float64
	// ConfigBase carries provider/budget/backend config; the runner overlays the
	// per-run Interviewer, EventHandler, and Params onto a copy of it.
	ConfigBase tracker.Config
}

// Runner maps Slack threads to tracker runs. It owns a RunManager and, per
// active thread, the ThreadInterviewer that inbound interactions resolve against.
type Runner struct {
	rm   *tracker.RunManager
	deps RunnerDeps

	mu       sync.Mutex
	byThread map[string]*ThreadInterviewer // thread_ts → interviewer (inbound routing)
}

// NewRunner builds a Runner over an existing RunManager.
func NewRunner(rm *tracker.RunManager, deps RunnerDeps) *Runner {
	return &Runner{rm: rm, deps: deps, byThread: make(map[string]*ThreadInterviewer)}
}

// OnMention starts a run for a fresh @mention. thread_ts is the run's identity:
// the RunManager keys on it, and every message for this run routes by it.
func (r *Runner) OnMention(ctx context.Context, channel, threadTS, text string) {
	ui := r.deps.NewThreadUI(channel, threadTS)

	// A mention may be a control command (help/status/cancel/runs) rather than a
	// request to start a run — including inside an already-active thread.
	if r.handleCommand(ui, threadTS, text) {
		return
	}

	intent, err := r.resolveIntent(ctx, text)
	if err != nil {
		_ = ui.Post("I couldn't work out what to run: " + err.Error())
		return
	}
	if !validWorkflowName(intent.Workflow) {
		// SECURITY: reject path-shaped names before ResolveSource. ResolveSource
		// treats a name with a separator or .dip suffix as a filesystem path
		// (and absolute paths as-is), so an unvalidated name like
		// "../../../etc/hosts" or "/abs/evil.dip" would load an arbitrary file
		// off the host. The LLM resolver validates against the catalog; the
		// grammar fallback does not — this guard covers both uniformly.
		_ = ui.Post(fmt.Sprintf("`%s` isn't a valid workflow name — try a built-in, or `runs` to see what's available.", intent.Workflow))
		return
	}
	source, info, err := tracker.ResolveSource(intent.Workflow, r.deps.WorkDir)
	if err != nil {
		_ = ui.Post("Unknown workflow: " + err.Error())
		return
	}

	if !r.estimateAndConfirm(ctx, ui, threadTS, source) {
		return
	}

	rec := RunRecord{ThreadTS: threadTS, Channel: channel, Workflow: intent.Workflow, Params: intent.Params}
	r.launch(ctx, ui, source, rec,
		fmt.Sprintf("🚀 starting `%s` — I'll keep you posted here.", info.DisplayName))
}

// estimateAndConfirm posts a rough cost estimate up front (the "tells you the
// bill before you spend" signal) and, above the configured threshold, blocks on
// a confirm gate. Returns false only when the human declined. A failed estimate
// never blocks a run.
func (r *Runner) estimateAndConfirm(ctx context.Context, ui ThreadUI, threadTS, source string) bool {
	est, err := tracker.EstimateRun(ctx, source)
	if err != nil || est.AgentNodes == 0 {
		return true
	}
	_ = ui.Post(fmt.Sprintf("🔎 Estimate: ~$%.2f–$%.2f · %d steps · %d agent nodes _(rough — actual depends on turns/loops)_",
		est.LowUSD, est.HighUSD, est.Steps, est.AgentNodes))
	if r.needsConfirm(est) {
		return r.confirmRun(ctx, ui, threadTS, est)
	}
	return true
}

// needsConfirm reports whether the estimate exceeds the confirm-over threshold.
func (r *Runner) needsConfirm(est *tracker.RunEstimate) bool {
	return r.deps.ConfirmOverUSD > 0 && est.ExpectedUSD >= r.deps.ConfirmOverUSD
}

// confirmRun posts a Run/Cancel gate and blocks until the human answers, using a
// temporary interviewer registered for the thread just for this decision (the
// run's own interviewer is created later in launch). Returns true to proceed.
func (r *Runner) confirmRun(ctx context.Context, ui ThreadUI, threadTS string, est *tracker.RunEstimate) bool {
	iv := NewThreadInterviewer(ui, r.deps.NewID)
	iv.SetPipelineContext(ctx)
	r.register(threadTS, iv)
	ans, err := iv.Ask(
		fmt.Sprintf("This looks like ~$%.2f (up to $%.2f). Run it?", est.ExpectedUSD, est.HighUSD),
		[]string{"Run it", "Cancel"}, "Run it")
	r.unregister(threadTS)
	iv.Cancel()
	if err != nil || ans != "Run it" {
		_ = ui.Post("👍 cancelled — no run started.")
		return false
	}
	return true
}

// Resume re-launches an interrupted run after a restart. Each thread has a
// deterministic workdir + checkpoint path; because the checkpoint file still
// exists, launching again replays from it (the engine loads a checkpoint at its
// configured path automatically). No run-id bookkeeping required.
func (r *Runner) Resume(ctx context.Context, rec RunRecord) {
	ui := r.deps.NewThreadUI(rec.Channel, rec.ThreadTS)
	if !validWorkflowName(rec.Workflow) {
		// A persisted record with a path-shaped workflow (tampered state file)
		// must not reach ResolveSource — same containment as OnMention.
		_ = ui.Post("Couldn't resume (invalid workflow name in saved state).")
		r.deps.Store.remove(rec.ThreadTS)
		return
	}
	source, _, err := tracker.ResolveSource(rec.Workflow, r.deps.WorkDir)
	if err != nil {
		_ = ui.Post("Couldn't resume (workflow gone): " + err.Error())
		r.deps.Store.remove(rec.ThreadTS)
		return
	}
	r.launch(ctx, ui, source, rec, "🔄 resuming this run after a restart…")
}

// launch wires a per-run interviewer + notifier onto a copy of the base config,
// pins the deterministic per-thread workdir + checkpoint path (so the run is
// resumable and so a resume replays from it), starts the run, records it, and
// watches it to completion.
func (r *Runner) launch(ctx context.Context, ui ThreadUI, source string, rec RunRecord, ack string) {
	workDir, checkpoint := r.runPaths(rec.ThreadTS)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		_ = ui.Post("Couldn't prepare a workspace: " + err.Error())
		return
	}

	iv := NewThreadInterviewer(ui, r.deps.NewID)
	cfg := r.deps.ConfigBase
	cfg.WorkingDir = workDir
	cfg.CheckpointDir = checkpoint
	cfg.Params = rec.Params
	cfg.Interviewer = iv

	// The notifier posts discrete gate/failure/terminal messages. When the
	// transport supports a live status card (StatusRenderer), also drive that
	// card from the stream and quiet the notifier's per-stage/cost chatter (the
	// card shows it) — the thread gets one updating dashboard instead of spam.
	nf := newNotifier(ui)
	handler := nf.HandlePipelineEvent
	if sr, ok := ui.(StatusRenderer); ok {
		st := newStatusTracker(sr, rec.Workflow, float64(cfg.Budget.MaxCostCents)/100)
		nf.quiet = true
		handler = func(evt pipeline.PipelineEvent) {
			st.HandlePipelineEvent(evt)
			nf.HandlePipelineEvent(evt)
		}
	}
	cfg.EventHandler = pipeline.PipelineEventHandlerFunc(handler)

	// Register AFTER a successful Start: registering earlier would let a duplicate
	// mention that Start rejects (ErrRunKeyActive) clobber the live run's
	// interviewer. The window before register is far shorter than the run
	// reaching its first gate, so no inbound answer is lost in practice.
	run, err := r.rm.Start(ctx, rec.ThreadTS, source, cfg)
	if err != nil {
		r.handleAdmission(ui, err)
		return
	}
	r.register(rec.ThreadTS, iv)
	r.deps.Store.put(rec)
	if cents := cfg.Budget.MaxCostCents; cents > 0 {
		ack += fmt.Sprintf("\n_Budget: up to $%.2f._", float64(cents)/100)
	}
	_ = ui.Post(ack)
	go r.watch(rec.ThreadTS, run, ui)
}

// runPaths returns the deterministic workdir and checkpoint path for a thread.
func (r *Runner) runPaths(threadTS string) (workDir, checkpoint string) {
	workDir = filepath.Join(r.deps.RunsBase, sanitizeThread(threadTS))
	return workDir, filepath.Join(workDir, "checkpoint.json")
}

// SweepOrphans removes workdirs under RunsBase that no live store record
// references — left by a crash between a run's store.remove and its reap. Runs
// referenced by keep (the current store records, which Resume will replay) are
// preserved. Best-effort; the state file (not a dir) is skipped.
func (r *Runner) SweepOrphans(keep []RunRecord) {
	live := make(map[string]bool, len(keep))
	for _, rec := range keep {
		live[sanitizeThread(rec.ThreadTS)] = true
	}
	entries, err := os.ReadDir(r.deps.RunsBase)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || live[e.Name()] {
			continue
		}
		_ = os.RemoveAll(filepath.Join(r.deps.RunsBase, e.Name()))
	}
}

// workflowNamePattern matches a safe bare workflow identifier — no path
// separators, no dots — so it can never be interpreted as a filesystem path by
// ResolveSource. Built-in names and local <name>.dip files resolve by this bare
// name; a user types "quick", not "quick.dip" or a path.
var workflowNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// validWorkflowName reports whether name is a safe bare workflow identifier.
func validWorkflowName(name string) bool {
	return workflowNamePattern.MatchString(name)
}

// sanitizeThread turns a thread_ts into a safe single path segment.
func sanitizeThread(threadTS string) string {
	out := strings.Map(func(rn rune) rune {
		switch {
		case rn >= 'a' && rn <= 'z', rn >= 'A' && rn <= 'Z', rn >= '0' && rn <= '9', rn == '-', rn == '_':
			return rn
		default:
			return '_'
		}
	}, threadTS)
	if out == "" {
		return "thread"
	}
	return out
}

const helpText = "*trackerbot* — run Tracker pipelines from Slack.\n" +
	"• `@trackerbot <what you want>` — start a run (I'll pick a workflow), or `run <workflow> [k=v …]`\n" +
	"• `@trackerbot status` — this thread's run state\n" +
	"• `@trackerbot cancel` — stop this thread's run\n" +
	"• `@trackerbot runs` — list active runs\n" +
	"• `@trackerbot help` — this message"

// handleCommand handles control verbs (help/status/cancel/runs). Returns true if
// the mention was a command and no run should be started.
func (r *Runner) handleCommand(ui ThreadUI, threadTS, text string) bool {
	switch strings.ToLower(strings.TrimSpace(stripMention(text))) {
	case "", "help", "?":
		_ = ui.Post(helpText)
	case "status":
		r.postStatus(ui, threadTS)
	case "cancel", "stop":
		r.postCancel(ui, threadTS)
	case "runs", "list":
		r.postRuns(ui)
	default:
		return false
	}
	return true
}

func (r *Runner) postStatus(ui ThreadUI, threadTS string) {
	run, ok := r.rm.Get(threadTS)
	if !ok {
		_ = ui.Post("No run in this thread.")
		return
	}
	_ = ui.Post(fmt.Sprintf("Status: *%s*%s", run.State(), runIDSuffix(run.RunID())))
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

// resolveIntent uses the configured IntentResolver, or the grammar by default.
func (r *Runner) resolveIntent(ctx context.Context, text string) (Intent, error) {
	if r.deps.Intent != nil {
		return r.deps.Intent.Resolve(ctx, text)
	}
	return parseGrammar(text)
}

// OnInteraction routes an inbound button/modal/reply to the right run's pending
// gate, using thread_ts (which run) and gateID (which gate). Returns false if no
// run/gate matched.
func (r *Runner) OnInteraction(threadTS, gateID string, answer GateAnswer) bool {
	r.mu.Lock()
	iv := r.byThread[threadTS]
	r.mu.Unlock()
	if iv == nil {
		return false
	}
	return iv.Resolve(gateID, answer)
}

// handleAdmission applies the already-active / at-capacity policy.
//
// DECISION POINT (D4) — at capacity this starter simply rejects. Alternatives:
// queue the request and start it when a slot frees, or preempt the oldest run.
func (r *Runner) handleAdmission(ui ThreadUI, err error) {
	switch {
	case errors.Is(err, tracker.ErrRunKeyActive):
		_ = ui.Post("A run is already active in this thread — reply here, or open a new thread for a new run.")
	case errors.Is(err, tracker.ErrAtCapacity):
		_ = ui.Post("I'm at capacity right now — please try again in a bit.")
	default:
		_ = ui.Post("Couldn't start the run: " + err.Error())
	}
}

// watch waits for a run to finish, unregisters it, delivers the outcome, and
// reaps the run's disk. Reaping runs after deliver so delivery can still read
// the run's artifacts.
func (r *Runner) watch(threadTS string, run *tracker.ManagedRun, ui ThreadUI) {
	<-run.Done()
	r.unregister(threadTS)
	r.deps.Store.remove(threadTS) // finished — no longer resumable
	r.rm.Forget(threadTS)         // free the thread for a future run
	deliver(context.Background(), ui, run)
	r.reap(threadTS)
}

// reap reclaims a finished run's workdir (bounding disk), or, when retention is
// on, drops just the checkpoint so a later run in the same thread starts fresh
// instead of replaying this one. os.RemoveAll ignores ErrNotExist, so a
// concurrent cleanup is harmless. (A crash before reap leaves the workdir +
// checkpoint for resume.)
func (r *Runner) reap(threadTS string) {
	workDir, checkpoint := r.runPaths(threadTS)
	if r.deps.KeepWorkdirs {
		if checkpoint != "" {
			_ = os.Remove(checkpoint)
		}
		return
	}
	_ = os.RemoveAll(workDir)
}

func (r *Runner) register(threadTS string, iv *ThreadInterviewer) {
	r.mu.Lock()
	r.byThread[threadTS] = iv
	r.mu.Unlock()
}

func (r *Runner) unregister(threadTS string) {
	r.mu.Lock()
	delete(r.byThread, threadTS)
	r.mu.Unlock()
}
