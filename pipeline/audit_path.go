// ABOUTME: Resolves the on-disk location of the integrity-protected activity log.
// ABOUTME: Threat model for the relocation lives in CLAUDE.md (#213).
package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ActivityLogSentinel is the two-byte prefix the runtime writes ahead of
// every JSONL line in the secure activity log. The bytes are ASCII Unit
// Separator (0x1F) and Record Separator (0x1E) — control characters that
// a normal text-mode subprocess effectively never emits. Their presence
// is the runtime's "I wrote this" mark; their absence on a line in the
// secure file is the injection signal.
//
// This is detection, not authentication: the bytes are not secret and an
// attacker who reads tracker's source (open source) can emit them too.
// See the "Activity log integrity" section of CLAUDE.md for the threat
// model and the intentional scope of this defense.
const ActivityLogSentinel = "\x1f\x1e"

// auditDirEnvVar overrides the default secure-log base directory. Set to
// an absolute path; runs land under <override>/<runID>/activity.jsonl.
// Used by tests and embedders that want to pin a custom location.
const auditDirEnvVar = "TRACKER_AUDIT_DIR"

// xdgStateHomeEnvVar is the XDG Base Directory env var that, when set,
// determines where state-class user data lives. Default (per XDG spec)
// is $HOME/.local/state.
const xdgStateHomeEnvVar = "XDG_STATE_HOME"

// SecureActivityLogPath returns the absolute path where the runtime
// writes the integrity-protected activity log for runID. The path is
// outside any directory a tool subprocess sees as cmd.Dir, so the most
// common LLM-tool-mistake attack vectors (relative-path shell
// redirection, find-in-cwd globs) cannot reach it.
//
// Resolution order:
//
//  1. $TRACKER_AUDIT_DIR/<runID>/activity.jsonl — explicit operator override.
//  2. $XDG_STATE_HOME/tracker/runs/<runID>/activity.jsonl — when XDG_STATE_HOME is set.
//  3. $HOME/.local/state/tracker/runs/<runID>/activity.jsonl — Linux + macOS default.
//  4. %LOCALAPPDATA%\tracker\runs\<runID>\activity.jsonl — Windows fallback.
//  5. os.TempDir()/tracker-audit/<runID>/activity.jsonl — last-resort when no $HOME (containers, restricted envs).
//
// runID must be a single clean path element — no separators, no "..",
// no ".". This guards against tampered checkpoints that could try to
// escape the secure base via path traversal (the resume path reads
// runID from <workDir>/.tracker/runs/<runID>/checkpoint.json, which is
// attacker-reachable).
//
// $TRACKER_AUDIT_DIR and $XDG_STATE_HOME must be absolute paths. A
// relative value would re-anchor the "secure" path to the process CWD
// — defeating the relocation entirely — so a relative env value is
// silently ignored and the resolver falls through to the next
// candidate.
func SecureActivityLogPath(runID string) (string, error) {
	if err := validateRunID(runID); err != nil {
		return "", err
	}
	base, err := secureActivityLogBase()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, runID, "activity.jsonl"), nil
}

// validateRunID enforces that runID is safe to interpolate into the
// secure path. Allowed: non-empty, equals its own filepath.Base,
// contains no separator, no "..", no ".". On Windows we also reject
// drive-letter forms and any backslash since filepath.Base behaves
// differently across OSes.
func validateRunID(runID string) error {
	if runID == "" {
		return fmt.Errorf("secure activity log path: empty runID")
	}
	if strings.ContainsAny(runID, `/\`) {
		return fmt.Errorf("secure activity log path: runID %q must not contain path separators", runID)
	}
	if runID == "." || runID == ".." {
		return fmt.Errorf("secure activity log path: runID %q is a path traversal", runID)
	}
	if filepath.Base(runID) != runID {
		return fmt.Errorf("secure activity log path: runID %q is not a single path element", runID)
	}
	return nil
}

func secureActivityLogBase() (string, error) {
	if v := absEnv(auditDirEnvVar); v != "" {
		return v, nil
	}
	if v := absEnv(xdgStateHomeEnvVar); v != "" {
		return filepath.Join(v, "tracker", "runs"), nil
	}
	if runtime.GOOS == "windows" {
		if v := absEnv("LOCALAPPDATA"); v != "" {
			return filepath.Join(v, "tracker", "runs"), nil
		}
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" && filepath.IsAbs(home) {
		return filepath.Join(home, ".local", "state", "tracker", "runs"), nil
	}
	// $HOME unresolvable or non-absolute (e.g. some minimal containers).
	// Fall back to a per-user-ish temp dir under os.TempDir(), which is
	// always absolute by the platform contract. This still keeps the log
	// outside cmd.Dir, which is the load-bearing property for the
	// relative-path threat.
	return filepath.Join(os.TempDir(), "tracker-audit"), nil
}

// absEnv reads an env var and returns it only if it's a non-empty
// absolute path. Relative values are silently ignored so a
// misconfiguration like TRACKER_AUDIT_DIR=.tracker/runs can't re-land
// the secure log under the process CWD where tool subprocesses with
// cmd.Dir = workDir could reach it.
func absEnv(name string) string {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" || !filepath.IsAbs(v) {
		return ""
	}
	return v
}
