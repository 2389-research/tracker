// ABOUTME: The end-of-run notice: a workflow's short next-steps text from <run dir>/notice.txt,
// ABOUTME: printed after the summary, bounded and stripped of control characters.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// noticeLimit bounds what a workflow can print at the end of a run.
const noticeLimit = 4096

// printRunNotice prints <runDir>/notice.txt, the short message a workflow
// leaves for the user (explain's answer path and next step, quickfix's
// branch and merge commands). A missing file prints nothing. The text is
// written by the workflow's tool nodes, so it is bounded and stripped of
// control characters other than newline and tab before it reaches the
// terminal.
func printRunNotice(runDir string) {
	f, err := os.Open(filepath.Join(runDir, "notice.txt"))
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, noticeLimit))
	text := strings.TrimRight(sanitizeNotice(string(b)), "\n")
	if text == "" {
		return
	}
	fmt.Println()
	fmt.Println(text)
}

// sanitizeNotice drops control characters (ANSI escapes included) except
// newline and tab.
func sanitizeNotice(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || (r >= 0x20 && r != 0x7f && !(r >= 0x80 && r < 0xa0)) {
			return r
		}
		return -1
	}, s)
}

// resolveArtifactDir returns the configured artifact dir, defaulting to
// <workdir>/.tracker/runs when none was set. Extracted from run/runTUI for the
// complexity gate.
func resolveArtifactDir(workdir, artifactDir string) string {
	if artifactDir != "" {
		return artifactDir
	}
	return filepath.Join(workdir, ".tracker", "runs")
}
