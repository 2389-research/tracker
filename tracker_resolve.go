// ABOUTME: Library-facing helpers for resolving pipeline sources and run checkpoints.
// ABOUTME: Mirrors the CLI's bare-name and run-ID resolution for library consumers.
package tracker

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/2389-research/tracker/internal/diag"
	"github.com/2389-research/tracker/pipeline"
)

// ResolveSource resolves a pipeline name to the actual workflow source text.
// The resolution order matches the CLI's tracker.resolvePipelineSource:
//
//  1. If name contains "/" or "\" (forward or back slash) or ends in
//     ".dip"/".dipx"/".dot", treat it as a filesystem path and read it
//     from disk.
//  2. If "<name>.dip" exists under workDir, read that.
//  3. If "<name>" exists under workDir, read that.
//  4. If name matches a built-in workflow, return the embedded source.
//  5. Otherwise return an error listing available built-ins.
//
// For a built-in the returned WorkflowInfo is the catalog entry; for a
// filesystem source only WorkflowInfo.Path is set (the resolved path the text
// was read from). Either way info.Ref() is the SourceRef to pass on
// (Config.Source / WithSource / WithValidateSource) so the source's *_file
// directives resolve against where it actually lives, not the process cwd.
// workDir may be empty — in that case the current working directory is used
// for relative lookups.
func ResolveSource(name, workDir string) (source string, info WorkflowInfo, err error) {
	if name == "" {
		return "", WorkflowInfo{}, fmt.Errorf("pipeline name cannot be empty")
	}

	if isExplicitFilePath(name) {
		path := name
		if !filepath.IsAbs(path) && workDir != "" {
			path = filepath.Join(workDir, name)
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return "", WorkflowInfo{}, fmt.Errorf("read pipeline file %q: %w", path, rerr)
		}
		return string(data), WorkflowInfo{Path: path}, nil
	}

	baseDir := workDir
	if baseDir == "" {
		if cwd, cerr := os.Getwd(); cerr == nil {
			baseDir = cwd
		}
	}

	for _, candidate := range []string{filepath.Join(baseDir, name+".dip"), filepath.Join(baseDir, name)} {
		data, found, rerr := readIfExists(candidate)
		if rerr != nil {
			return "", WorkflowInfo{}, rerr
		}
		if found {
			return string(data), WorkflowInfo{Path: candidate}, nil
		}
	}

	if wfInfo, ok := LookupWorkflow(name); ok {
		data, _, oerr := OpenWorkflow(name)
		if oerr != nil {
			return "", wfInfo, oerr
		}
		return string(data), wfInfo, nil
	}

	return "", WorkflowInfo{}, buildPipelineNotFoundError(name)
}

// readIfExists reads path when it exists. found is false, with a nil error,
// when path does not exist; any other stat or read failure is returned.
func readIfExists(path string) (data []byte, found bool, err error) {
	if _, statErr := os.Stat(path); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("stat %q: %w", path, statErr)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("read %q: %w", path, err)
	}
	return data, true, nil
}

// isExplicitFilePath returns true if name looks like a filesystem path rather
// than a bare workflow name.
func isExplicitFilePath(name string) bool {
	return strings.ContainsAny(name, "/\\") || strings.HasSuffix(name, ".dip") || strings.HasSuffix(name, ".dipx") || strings.HasSuffix(name, ".dot")
}

// buildPipelineNotFoundError returns a diagnostic listing all available
// built-in workflows when a name can't be resolved.
func buildPipelineNotFoundError(name string) error {
	available := Workflows()
	if len(available) == 0 {
		return fmt.Errorf("unknown pipeline %q: file not found", name)
	}
	names := make([]string, 0, len(available))
	for _, wf := range available {
		names = append(names, wf.Name)
	}
	return fmt.Errorf(
		"unknown pipeline %q (no local file found, not a built-in workflow). Available built-in workflows: %s",
		name, strings.Join(names, ", "),
	)
}

// ResolveCheckpoint finds the checkpoint file path for a given run ID under
// the working directory's default artifact dir, <workDir>/.tracker/runs. The
// runID argument may be a unique prefix of a real run ID. It is
// ResolveCheckpointIn(filepath.Join(workDir, ".tracker", "runs"), runID); a run
// started with a custom artifact dir (--artifact-dir / Config.ArtifactDir)
// must be resolved with ResolveCheckpointIn and that dir instead.
//
// Unlike ResolveCheckpointIn, a full run ID whose run dir is absent from
// <workDir>/.tracker/runs (an excluded or cleaned snapshot dir) still resolves
// to its secure checkpoint, with a warning: the resumed run then writes its
// artifacts to <workDir>/.tracker/runs.
//
// This is the library equivalent of the CLI's `tracker -r <runID>` flag.
// Library consumers can set Config.ResumeRunID to have NewEngine resolve
// the checkpoint automatically.
func ResolveCheckpoint(workDir, runID string) (string, error) {
	return resolveCheckpointOrSecure(filepath.Join(workDir, ".tracker", "runs"), runID)
}

// ResolveCheckpointIn finds the checkpoint to resume runID from, where runsDir
// is the run's artifact dir (the directory holding <runID>/ run dirs: the
// --artifact-dir value, or <workDir>/.tracker/runs by default). runID may be a
// unique prefix of a run dir name in runsDir.
//
// The AUTHORITATIVE secure checkpoint (pipeline.SecureCheckpointPath, #559) is
// returned when it exists; the <runsDir>/<runID>/checkpoint.json snapshot is
// tool-reachable, so it is only the legacy fallback for runs with no secure
// copy. The run's dir must be in runsDir: when runsDir is unreadable or holds
// no match, it is an error even for a full run ID with a secure checkpoint.
// The checkpoint records neither the run's workdir nor its artifact dir, so
// resuming from a runsDir that does not hold the run would write its artifacts
// there and split the run across two dirs. Prefixes are matched in runsDir
// only, never in the secure base: that base is per-user and shared across
// projects, so a prefix there could resume another project's run.
func ResolveCheckpointIn(runsDir, runID string) (string, error) {
	if runID == "" {
		return "", fmt.Errorf("run ID cannot be empty")
	}
	resolved, err := resolveRunIDToDir(runsDir, runID)
	if err != nil {
		return "", err
	}
	snapshot := filepath.Join(runsDir, resolved, "checkpoint.json")
	// Prefer the AUTHORITATIVE secure checkpoint (#559): the artifact-dir copy
	// is a best-effort snapshot a tool subprocess could have tampered, so resume
	// must read the secure state-dir copy when present.
	if secure, ok := existingSecureCheckpoint(resolved); ok {
		warnIfSnapshotNewer(secure, snapshot)
		return secure, nil
	}
	// Legacy fallback: pre-#559 runs (or archive-moved) have only the snapshot.
	return legacyCheckpoint(snapshot, resolved)
}

// resolveCheckpointOrSecure is ResolveCheckpointIn plus the library's
// back-compat exact-ID fallback: when runsDir does not hold the run (its
// snapshot dir was excluded or cleaned) but runID is a full run ID with a
// secure checkpoint, resume from that. It warns, since the resumed run writes
// its artifacts to runsDir, which is wrong if the run used another artifact
// dir. Used by ResolveCheckpoint and Config.ResumeRunID; the CLI uses the
// strict ResolveCheckpointIn.
func resolveCheckpointOrSecure(runsDir, runID string) (string, error) {
	cpPath, err := ResolveCheckpointIn(runsDir, runID)
	if err == nil || runID == "" {
		return cpPath, err
	}
	if _, dirErr := resolveRunIDToDir(runsDir, runID); dirErr == nil {
		return "", err // the run dir is there; the failure is something else
	}
	secure, ok := existingSecureCheckpoint(runID)
	if !ok {
		return "", err
	}
	diag.Warnf("warning: run %s has no run dir in %s; resuming from its secure checkpoint %s, and the resumed run writes its artifacts (checkpoint snapshot, activity log, run.json, node outputs) to %s. If the run used another artifact dir or workdir, stop and resume with the same values",
		runID, runsDir, secure, filepath.Join(runsDir, runID))
	return secure, nil
}

// resumeRunsDir is the artifact dir a resumed run's <runID>/ dir lives in:
// Config.ArtifactDir when set, else the <workDir>/.tracker/runs default the
// CLI and run capture use (#696).
func resumeRunsDir(cfg Config, workDir string) string {
	if cfg.ArtifactDir != "" {
		return cfg.ArtifactDir
	}
	return filepath.Join(workDir, ".tracker", "runs")
}

// legacyCheckpoint returns the artifact-dir checkpoint for resolved when it
// exists, with a not-found error naming the run dir otherwise.
func legacyCheckpoint(cpPath, resolved string) (string, error) {
	if _, err := os.Stat(cpPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("checkpoint not found for run %s (no checkpoint.json in %s)", resolved, filepath.Dir(cpPath))
		}
		return "", fmt.Errorf("checkpoint for run %s: %w", resolved, err)
	}
	return cpPath, nil
}

// warnIfSnapshotNewer warns when the artifact-dir snapshot is newer than the
// secure checkpoint resume is about to read. Before #696 the CLI resume read
// and advanced only the snapshot, so a run resumed that way has a stale secure
// copy; resuming from it re-runs nodes the snapshot already passed. The secure
// copy still wins: preferring the snapshot would reopen the #559 tamper vector.
func warnIfSnapshotNewer(secure, snapshot string) {
	sec, err := pipeline.LoadCheckpoint(secure)
	if err != nil {
		return
	}
	// The snapshot is tool-writable: read it bounded and non-blocking, so a
	// FIFO or huge file planted there cannot hang or exhaust a resume whose
	// secure checkpoint is valid. Anything unreadable just skips the warning.
	snap, err := pipeline.LoadCheckpointBounded(snapshot, maxSnapshotInspectBytes)
	if err != nil || !snap.Timestamp.After(sec.Timestamp) {
		return
	}
	diag.Warnf("warning: resuming run %s from the authoritative checkpoint %s (saved %s); the artifact-dir snapshot %s is newer (saved %s) and is ignored, so nodes completed after the secure save will run again",
		sec.RunID, secure, sec.Timestamp.Format(time.RFC3339), snapshot, snap.Timestamp.Format(time.RFC3339))
}

// maxSnapshotInspectBytes caps how much of the untrusted artifact-dir snapshot
// warnIfSnapshotNewer reads. A real checkpoint is far smaller.
const maxSnapshotInspectBytes = 64 << 20

// existingSecureCheckpoint returns the secure checkpoint path for a full runID
// when it resolves and the file exists on disk.
func existingSecureCheckpoint(runID string) (string, bool) {
	secure, err := pipeline.SecureCheckpointPath(runID)
	if err != nil {
		return "", false
	}
	if _, err := os.Stat(secure); err != nil {
		return "", false
	}
	return secure, true
}

// runDirHint names the usual cause of a run missing from the runs dir: the run
// was started with a custom artifact dir or workdir that the resume did not
// repeat.
const runDirHint = " (if the run was started with --artifact-dir or -w / Config.ArtifactDir or WorkDir, resume with the same values)"

// missingRunsDirHint returns runDirHint for an absent runs dir.
func missingRunsDirHint(err error) string {
	if !errors.Is(err, os.ErrNotExist) {
		return ""
	}
	return runDirHint
}

// resolveRunIDToDir finds the unique run directory matching a run ID prefix.
func resolveRunIDToDir(runsDir, runID string) (string, error) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return "", fmt.Errorf("cannot read runs directory %q: %w%s", runsDir, err, missingRunsDirHint(err))
	}

	var matches []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), runID) {
			matches = append(matches, e.Name())
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no run found matching %q in %s%s", runID, runsDir, runDirHint)
	case 1:
		return matches[0], nil
	default:
		for _, m := range matches {
			if m == runID {
				return m, nil
			}
		}
		return "", fmt.Errorf("ambiguous run ID %q matches %d runs: %s", runID, len(matches), strings.Join(matches, ", "))
	}
}
