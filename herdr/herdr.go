// ABOUTME: Reports tracker's run lifecycle to a Herdr terminal pane manager.
// ABOUTME: Maps pipeline events to Herdr's working / blocked / idle agent states.
package herdr

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

// Herdr pane environment variables. A pane injects these into the agent it runs;
// see https://herdr.dev/docs/integrations/#integrate-your-own-agent.
const (
	envMarker  = "HERDR_ENV"      // "1" inside a herdr pane
	envPaneID  = "HERDR_PANE_ID"  // the pane to report against
	envBinPath = "HERDR_BIN_PATH" // the herdr CLI to invoke
	envDisable = "TRACKER_HERDR"  // "0" opts tracker out even inside a pane
)

// Reporting identity. The source must stay stable and unique to this integration
// so herdr attributes lifecycle authority to one reporter.
const (
	defaultSource = "custom:tracker"
	defaultAgent  = "tracker"
)

// reportTimeout bounds a single herdr CLI call so a wedged binary cannot stall
// the engine goroutine that delivers events.
const reportTimeout = 2 * time.Second

// messageMaxBytes truncates a gate label before it rides a --message flag.
const messageMaxBytes = 160

// Env is the herdr pane a Reporter targets.
type Env struct {
	PaneID  string
	BinPath string
}

// runnerFunc executes one herdr CLI invocation. It is injected so tests drive
// the reporter without spawning a subprocess.
type runnerFunc func(ctx context.Context, binPath string, args []string) error

// Reporter translates pipeline lifecycle events into herdr agent-state reports.
// It implements pipeline.PipelineEventHandler and is safe for concurrent use:
// events arrive on the engine goroutine while Release runs from a deferred
// caller.
type Reporter struct {
	env        Env
	source     string
	agent      string
	humanGates bool // report `blocked` only when a human answers gates
	run        runnerFunc

	mu        sync.Mutex
	seq       int
	state     string          // last reported state; "" before the first report
	openGates map[string]bool // GateIDs of open human gates
	runID     string
	released  bool
}

// Detect returns a Reporter when tracker runs inside a herdr pane
// (HERDR_ENV=1 with HERDR_PANE_ID and HERDR_BIN_PATH set) and reporting is not
// disabled via TRACKER_HERDR=0. It returns nil otherwise, so the integration is
// a no-op outside herdr. humanGates is true when a human answers the run's gates
// (interactive) and false under autopilot / auto-approve, where no user decision
// is ever pending.
func Detect(humanGates bool) *Reporter {
	if os.Getenv(envMarker) != "1" {
		return nil
	}
	if os.Getenv(envDisable) == "0" {
		return nil
	}
	paneID := os.Getenv(envPaneID)
	binPath := os.Getenv(envBinPath)
	if paneID == "" || binPath == "" {
		return nil
	}
	return newReporter(Env{PaneID: paneID, BinPath: binPath}, humanGates, execRunner)
}

func newReporter(env Env, humanGates bool, run runnerFunc) *Reporter {
	return &Reporter{
		env:        env,
		source:     defaultSource,
		agent:      defaultAgent,
		humanGates: humanGates,
		run:        run,
		openGates:  map[string]bool{},
	}
}

// HandlePipelineEvent maps one pipeline event to a herdr state report.
func (r *Reporter) HandlePipelineEvent(evt pipeline.PipelineEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if isTopLevelFinish(evt) {
		r.report("idle", "")
		return
	}

	switch evt.Type {
	case pipeline.EventPipelineStarted:
		r.onStarted(evt)
	case pipeline.EventGateOpened:
		r.onGateOpened(evt)
	case pipeline.EventGateResolved:
		r.onGateResolved(evt)
	}
}

// isTopLevelFinish reports whether evt is the run's own terminal event. A scoped
// "parent/child" node id is a subgraph child that tripped the shared budget
// guard, not the top-level finish.
func isTopLevelFinish(evt pipeline.PipelineEvent) bool {
	return evt.TerminalStatus != "" && !strings.Contains(evt.NodeID, "/")
}

// onStarted records the run id and reports working. Caller holds r.mu.
func (r *Reporter) onStarted(evt pipeline.PipelineEvent) {
	if r.runID == "" {
		r.runID = evt.RunID
	}
	r.report("working", "")
}

// onGateOpened blocks the pane on a human gate. Caller holds r.mu.
func (r *Reporter) onGateOpened(evt pipeline.PipelineEvent) {
	if !r.humanGates || evt.Gate == nil {
		return
	}
	r.openGates[evt.Gate.GateID] = true
	r.report("blocked", gateMessage(evt.Gate))
}

// onGateResolved clears a resolved gate and reports working once no human gate
// is still open (parallel branches can hold several at once). Caller holds r.mu.
func (r *Reporter) onGateResolved(evt pipeline.PipelineEvent) {
	if !r.humanGates || evt.Gate == nil {
		return
	}
	delete(r.openGates, evt.Gate.GateID)
	if len(r.openGates) == 0 {
		r.report("working", "")
	}
}

// Release surrenders this source's lifecycle authority. It is idempotent and
// meant to be deferred at run teardown.
func (r *Reporter) Release() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.released {
		return
	}
	r.released = true
	args := []string{"pane", "release-agent", r.env.PaneID, "--source", r.source, "--agent", r.agent}
	r.invoke(args)
}

// report emits a report-agent call for a state transition. Repeated states are
// deduped so the pane sees one report per change. Caller holds r.mu.
func (r *Reporter) report(state, message string) {
	if state == r.state {
		return
	}
	r.state = state
	r.seq++
	args := []string{
		"pane", "report-agent", r.env.PaneID,
		"--source", r.source,
		"--agent", r.agent,
		"--state", state,
		"--seq", strconv.Itoa(r.seq),
	}
	if r.runID != "" {
		args = append(args, "--agent-session-id", r.runID)
	}
	if message != "" {
		args = append(args, "--message", message)
	}
	r.invoke(args)
}

// invoke runs one herdr CLI call best-effort: a failure never fails the run.
// Caller holds r.mu.
func (r *Reporter) invoke(args []string) {
	ctx, cancel := context.WithTimeout(context.Background(), reportTimeout)
	defer cancel()
	_ = r.run(ctx, r.env.BinPath, args)
}

// gateMessage is the short human-readable description of a blocking gate.
func gateMessage(gate *pipeline.GateDetail) string {
	msg := gate.Label
	if msg == "" {
		msg = gate.Prompt
	}
	return truncate(msg, messageMaxBytes)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// execRunner is the default runner: it invokes the herdr CLI directly (no shell,
// so gate text cannot inject arguments).
func execRunner(ctx context.Context, binPath string, args []string) error {
	return exec.CommandContext(ctx, binPath, args...).Run()
}
