// ABOUTME: Posts a run's outcome to its thread (decision D3: adapt to what was built).
package main

import (
	"fmt"

	tracker "github.com/2389-research/tracker"
	"github.com/2389-research/tracker/pipeline"
)

// deliver posts a finished run's outcome to its thread.
//
// DECISION POINT (D3) — this starter posts status + cost + the artifact dir. The
// richer goal is to adapt delivery to WHAT was built: a deploy URL in the run
// context → post the link; a git repo in the workdir → push to GitHub or attach
// a bundle; documents → upload the key files inline. The inputs to branch on are
// res.Context (workflow-written keys), res.ArtifactRunDir, and res.Status.
func deliver(ui ThreadUI, res *tracker.Result, runErr error) {
	if res == nil {
		_ = ui.Post(fmt.Sprintf("❌ the run could not start: %v", runErr))
		return
	}
	if pipeline.TerminalStatus(res.Status).IsSuccess() {
		cost := 0.0
		if res.Cost != nil {
			cost = res.Cost.TotalUSD
		}
		_ = ui.Post(fmt.Sprintf("🏁 done — *%s* ($%.2f spent).\nArtifacts: `%s`", res.Status, cost, res.ArtifactRunDir))
		return
	}
	_ = ui.Post(fmt.Sprintf("❌ run finished *%s*. I can pull a diagnosis if you want.", res.Status))
}
