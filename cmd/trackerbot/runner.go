// ABOUTME: Runner maps Slack threads to concurrent tracker runs via the RunManager.
// ABOUTME: OnMention starts a run; OnInteraction routes a reply/click to the run's gate.
package main

import (
	"context"
	"errors"
	"fmt"
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
	// resolve regardless). Per-run execution dirs are isolated by the RunManager.
	WorkDir string
	// NewID returns a fresh unique gate id per call.
	NewID func() string
	// Intent resolves @mention text to a workflow + params. Nil falls back to the
	// deterministic grammar ("[run] <workflow> [k=v ...]").
	Intent IntentResolver
	// ConfigBase carries provider/budget/backend config; the runner overlays the
	// per-run Interviewer, EventHandler, and Params onto a copy of it.
	ConfigBase tracker.Config
}

// Runner maps Slack threads to tracker runs. It owns a RunManager and, per
// active thread, the SlackInterviewer that inbound interactions resolve against.
type Runner struct {
	rm   *tracker.RunManager
	deps RunnerDeps

	mu       sync.Mutex
	byThread map[string]*SlackInterviewer // thread_ts → interviewer (inbound routing)
}

// NewRunner builds a Runner over an existing RunManager.
func NewRunner(rm *tracker.RunManager, deps RunnerDeps) *Runner {
	return &Runner{rm: rm, deps: deps, byThread: make(map[string]*SlackInterviewer)}
}

// OnMention starts a run for a fresh @mention. thread_ts is the run's identity:
// the RunManager keys on it, and every message for this run routes by it.
func (r *Runner) OnMention(ctx context.Context, channel, threadTS, text string) {
	ui := r.deps.NewThreadUI(channel, threadTS)

	intent, err := r.resolveIntent(ctx, text)
	if err != nil {
		_ = ui.Post("I couldn't work out what to run: " + err.Error())
		return
	}
	source, info, err := tracker.ResolveSource(intent.Workflow, r.deps.WorkDir)
	if err != nil {
		_ = ui.Post("Unknown workflow: " + err.Error())
		return
	}

	iv := NewSlackInterviewer(ui, r.deps.NewID)
	nf := newNotifier(ui)

	cfg := r.deps.ConfigBase
	cfg.WorkingDir = "" // let the RunManager assign an isolated per-thread workdir
	cfg.Interviewer = iv
	cfg.EventHandler = pipeline.PipelineEventHandlerFunc(nf.HandlePipelineEvent)
	cfg.Params = intent.Params

	run, err := r.rm.Start(ctx, threadTS, source, cfg)
	if err != nil {
		r.handleAdmission(ui, err)
		return
	}
	r.register(threadTS, iv)
	_ = ui.Post(fmt.Sprintf("🚀 starting `%s` — I'll keep you posted here.", info.DisplayName))
	go r.watch(threadTS, run, ui)
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

// watch waits for a run to finish, unregisters it, and delivers the outcome.
func (r *Runner) watch(threadTS string, run *tracker.ManagedRun, ui ThreadUI) {
	<-run.Done()
	r.unregister(threadTS)
	res, runErr := run.Result()
	deliver(ui, res, runErr)
}

func (r *Runner) register(threadTS string, iv *SlackInterviewer) {
	r.mu.Lock()
	r.byThread[threadTS] = iv
	r.mu.Unlock()
}

func (r *Runner) unregister(threadTS string) {
	r.mu.Lock()
	delete(r.byThread, threadTS)
	r.mu.Unlock()
}
