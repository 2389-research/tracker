// ABOUTME: Resume setup for the library: resolving Config.ResumeRunID to a
// ABOUTME: checkpoint, and the run lock a resumed run holds until Close.
package tracker

import (
	"fmt"
	"os"

	"github.com/2389-research/tracker/pipeline"
)

// applyResumeRunID resolves Config.ResumeRunID to a concrete checkpoint path
// and stores it on Config.CheckpointDir. A non-empty CheckpointDir on the
// incoming config is honored as an explicit override — the user is telling
// us exactly which file to use.
func applyResumeRunID(cfg *Config, workDir string) error {
	if cfg.ResumeRunID == "" || cfg.CheckpointDir != "" {
		return nil
	}
	cpPath, err := resolveCheckpointOrSecure(resumeRunsDir(*cfg, workDir), cfg.ResumeRunID)
	if err != nil {
		return fmt.Errorf("resume run %q: %w", cfg.ResumeRunID, err)
	}
	cfg.CheckpointDir = cpPath
	return nil
}

// resolveWorkDir returns the working directory, falling back to cwd if empty.
func resolveWorkDir(workDir string) (string, error) {
	if workDir != "" {
		return workDir, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	return dir, nil
}

// acquireResumeLock takes the run lock for a resumed run — one whose
// checkpoint (Config.ResumeRunID resolved to Config.CheckpointDir, or a
// CheckpointDir the caller passed, as the CLI's -r does) already exists — so
// two processes (two `tracker answer`s, or an answer and a `tracker -r`) never
// resume the same run at once. A fresh run has nothing to lock and gets a
// no-op release.
func acquireResumeLock(cfg Config) (func(), error) {
	if cfg.CheckpointDir == "" {
		return func() {}, nil
	}
	cp, err := pipeline.LoadCheckpoint(cfg.CheckpointDir)
	if err != nil || cp.RunID == "" {
		return func() {}, nil // the engine reports an unreadable checkpoint itself
	}
	release, err := pipeline.AcquireRunLock(cp.RunID)
	if err != nil {
		return nil, fmt.Errorf("resume run %s: %w", cp.RunID, err)
	}
	return release, nil
}

// assembleLocked takes a resumed run's lock, then resolves the LLM client and
// builds the Engine, which releases the lock on Close (or here, on failure).
func assembleLocked(graph *pipeline.Graph, cfg Config, workDir string) (*Engine, error) {
	release, err := acquireResumeLock(cfg)
	if err != nil {
		return nil, err
	}
	eng, err := assemble(graph, cfg, workDir)
	if err != nil {
		release()
		return nil, err
	}
	eng.releaseLock = release
	return eng, nil
}

// assemble resolves the LLM client and builds the Engine.
func assemble(graph *pipeline.Graph, cfg Config, workDir string) (*Engine, error) {
	client, completer, err := resolveCompleter(cfg, graph)
	if err != nil {
		return nil, err
	}
	return buildEngine(graph, cfg, workDir, client, completer)
}
