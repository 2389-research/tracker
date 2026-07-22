// ABOUTME: `tracker status [runID]` — the agent-authored high-level timeline of a
// ABOUTME: run (what got done), read from the activity log without the firehose (#494).
package main

import (
	"fmt"

	tracker "github.com/2389-research/tracker"
	"github.com/charmbracelet/lipgloss"
)

// executeStatus prints a run's agent-authored status-update timeline. With no
// run ID it uses the most recent run in the working dir.
func executeStatus(cfg runConfig) error {
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
	entries, err := tracker.RunStatusTimeline(runDir)
	if err != nil {
		return err
	}
	printStatusTimeline(runID, entries)
	return nil
}

func printStatusTimeline(runID string, entries []tracker.StatusEntry) {
	header := lipgloss.NewStyle().Bold(true)
	fmt.Printf("%s\n", header.Render(fmt.Sprintf("Status timeline — run %s (%d update%s)", runID, len(entries), plural(len(entries)))))
	if len(entries) == 0 {
		fmt.Println("  No status updates recorded — the agent didn't call report_status on this run.")
		return
	}
	fmt.Println()
	nodeStyle := lipgloss.NewStyle().Foreground(colorSky)
	timeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	for _, e := range entries {
		fmt.Printf("  %s  %s\n      %s\n",
			timeStyle.Render(shortTime(e.Timestamp)),
			nodeStyle.Render(nodeTag(e.NodeID)),
			e.Text)
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func nodeTag(nodeID string) string {
	if nodeID == "" {
		return ""
	}
	return "[" + nodeID + "]"
}

// shortTime extracts HH:MM:SS from an RFC3339-ish timestamp, or returns it as-is.
func shortTime(ts string) string {
	if len(ts) >= 19 && ts[10] == 'T' {
		return ts[11:19]
	}
	return ts
}
