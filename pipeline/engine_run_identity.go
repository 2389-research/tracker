// ABOUTME: Seeds a run's audit identity (run id + per-run artifact dir) into the pipeline
// ABOUTME: context and creates that dir before the first node runs, so TRACKER_RUN_DIR exists (#686).
package pipeline

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
)

// seedRunIdentity writes InternalKeyRunID and InternalKeyArtifactDir into pctx
// and then ensures the effective run dir exists on disk. Extracted from
// initRunState (complexity gate). runID is the fresh id or the checkpoint's on
// resume.
func (e *Engine) seedRunIdentity(pctx *PipelineContext, runID string) error {
	pctx.SetInternal(InternalKeyRunID, runID) // see InternalKeyRunID
	if e.artifactDir != "" {
		pctx.SetInternal(InternalKeyArtifactDir, filepath.Join(e.artifactDir, runID))
	}
	// #657: a subgraph child engine inherits the parent run's audit identity so
	// its nodes' capture sidecars, tool TRACKER_RUN_DIR, and events correlate
	// with the parent run instead of a fresh id / empty dir (the parallel
	// handler already re-propagates the run id to its branches). Applied after
	// the engine's own seeding so the inherited value wins; the child's own
	// artifactDir is empty here, so this is the only source.
	if e.inheritedRunID != "" {
		pctx.SetInternal(InternalKeyRunID, e.inheritedRunID)
	}
	if e.inheritedArtifactDir != "" {
		pctx.SetInternal(InternalKeyArtifactDir, e.inheritedArtifactDir)
	}
	return ensureRunDir(pctx)
}

// ensureRunDir creates the effective per-run artifact dir before any node runs
// (#686). Before this, the dir only appeared as a side effect of writes made
// AFTER a node finished (checkpoint snapshot, stage artifacts), so the first
// node — a `start:` tool node, a resume entry under an explicit checkpoint
// path, or a subgraph child's first node — got a TRACKER_RUN_DIR that did not
// exist yet. Reading the effective key covers fresh, resumed, and inherited
// dirs alike. No artifact dir (a bare engine) creates nothing. A pre-existing
// dir is left as-is (MkdirAll is idempotent; its mode is not changed). Mode
// 0700 matches WriteSpecArtifacts and the activity-log snapshot, which
// tightens the dir to 0700 at close anyway.
func ensureRunDir(pctx *PipelineContext) error {
	dir, _ := pctx.GetInternal(InternalKeyArtifactDir)
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create run artifact dir %s: %w", dir, err)
	}
	return nil
}

// generateRunID creates a random 6-byte hex run identifier.
func generateRunID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}
	return fmt.Sprintf("%x", b)
}
