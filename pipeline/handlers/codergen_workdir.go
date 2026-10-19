// ABOUTME: The directory a codergen node's session runs in: its working_dir override
// ABOUTME: (a relative one under the run workdir) or the handler's workdir.
package handlers

import (
	"path/filepath"

	"github.com/2389-research/tracker/pipeline"
)

// effectiveWorkingDir returns the directory this node's session actually runs
// in: the per-node working_dir override when set, otherwise the handler
// default. Mirrors buildConfig's WorkingDir resolution (#347 — the runtime
// block must name THIS node's dir, and resolvePrompt runs before buildConfig).
// A relative override is under the run's workdir — where tool nodes run — not
// the tracker process's cwd, so `tracker -w <dir>` started elsewhere reaches
// the same directory.
func (h *CodergenHandler) effectiveWorkingDir(node *pipeline.Node) string {
	wd := node.AgentConfig(h.graphAttrs).WorkingDir
	switch {
	case wd == "":
		return h.workingDir
	case filepath.IsAbs(wd) || h.workingDir == "":
		return wd
	}
	return filepath.Join(h.workingDir, wd)
}
