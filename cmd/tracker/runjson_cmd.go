// ABOUTME: `tracker run-json [runID]` — assembles run.json for a run directory.
// ABOUTME: Works after the fact, so SIGKILLed runs and runs archived before the manifest existed both get one.
package main

import (
	"fmt"

	"github.com/2389-research/tracker"
	"github.com/2389-research/tracker/pipeline"
)

// executeRunJSON writes run.json for the named run (or the most recent one).
//
// Deliberately a separate command rather than only a run-completion hook: a run
// killed with SIGKILL runs no in-process finalizer, and every run archived
// before the manifest existed has none either. Being able to assemble one from
// whatever the run directory holds is what makes those runs analyzable at all,
// and it also means the manifest can be regenerated after the schema changes
// instead of being frozen at whatever shape was current when the run happened.
func executeRunJSON(cfg runConfig) error {
	runID := cfg.resumeID
	if runID == "" {
		id, err := tracker.MostRecentRunID(cfg.workdir)
		if err != nil {
			return fmt.Errorf("no runs found: %w", err)
		}
		runID = id
	}
	runDir, err := tracker.ResolveRunDir(cfg.workdir, runID)
	if err != nil {
		return err
	}

	manifest, err := pipeline.WriteRunManifest(runDir, runID)
	if err != nil {
		return fmt.Errorf("assemble run manifest: %w", err)
	}
	printRunManifestSummary(runDir, manifest)
	return nil
}

// printRunManifestSummary reports what landed. Terminal status is printed even
// when empty — an unset status means the process died before emitting a terminal
// event, which is a real finding about the run and not a gap to hide.
func printRunManifestSummary(runDir string, m pipeline.RunManifest) {
	status := m.TerminalStatus
	if status == "" {
		status = "(none recorded — run did not emit a terminal event)"
	}
	fmt.Printf("wrote %s/%s\n", runDir, pipeline.RunManifestFile)
	fmt.Printf("  run:      %s\n", m.RunID)
	fmt.Printf("  status:   %s\n", status)
	fmt.Printf("  nodes:    %d\n", len(m.Nodes))
	if m.Totals.LLMCalls > 0 {
		fmt.Printf("  llm:      %d calls, %d in / %d out tokens\n",
			m.Totals.LLMCalls, m.Totals.InputTokens, m.Totals.OutputTokens)
	}
	if m.Human.Gates > 0 || m.Human.Steering > 0 {
		fmt.Printf("  human:    %d gate events, %d steering\n", m.Human.Gates, m.Human.Steering)
	}
}
