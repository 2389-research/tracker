// ABOUTME: Read-only run snapshots for a transport dashboard (e.g. Slack App Home).
package chatops

// RunView is a read-only snapshot of an active run, for a transport that renders
// a standing dashboard (Slack App Home, a web status page). It is decoupled from
// the engine's *ManagedRun so the transport layer needs no pipeline types.
type RunView struct {
	Key   string // external id — a Slack thread_ts
	State string // "starting" | "running" | terminal status
}

// ActiveRuns returns a snapshot of every run the Runner currently tracks, sorted
// by key (RunManager.List sorts). Safe to call from any goroutine.
func (r *Runner) ActiveRuns() []RunView {
	runs := r.rm.List()
	out := make([]RunView, 0, len(runs))
	for _, run := range runs {
		out = append(out, RunView{Key: run.Key, State: string(run.State())})
	}
	return out
}
