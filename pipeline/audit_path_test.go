package pipeline

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestSecureActivityLogPath_Override pins that TRACKER_AUDIT_DIR wins
// over every other resolution source.
func TestSecureActivityLogPath_Override(t *testing.T) {
	t.Setenv(auditDirEnvVar, "/tmp/custom-audit")
	t.Setenv(xdgStateHomeEnvVar, "/tmp/xdg-should-lose")
	t.Setenv("HOME", "/tmp/home-should-lose")

	got, err := SecureActivityLogPath("run-abc")
	if err != nil {
		t.Fatalf("SecureActivityLogPath: %v", err)
	}
	want := filepath.Join("/tmp/custom-audit", "run-abc", "activity.jsonl")
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

// TestSecureActivityLogPath_XDG pins that XDG_STATE_HOME is honored when
// TRACKER_AUDIT_DIR is unset.
func TestSecureActivityLogPath_XDG(t *testing.T) {
	t.Setenv(auditDirEnvVar, "")
	t.Setenv(xdgStateHomeEnvVar, "/var/lib/state")

	got, err := SecureActivityLogPath("run-abc")
	if err != nil {
		t.Fatalf("SecureActivityLogPath: %v", err)
	}
	want := filepath.Join("/var/lib/state", "tracker", "runs", "run-abc", "activity.jsonl")
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

// TestSecureActivityLogPath_HomeDefault pins the Linux+macOS default
// when neither override nor XDG is set. Skipped on Windows because
// secureActivityLogBase prefers LOCALAPPDATA before HOME there.
func TestSecureActivityLogPath_HomeDefault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("HOME default branch is unix-only; Windows uses LOCALAPPDATA")
	}
	t.Setenv(auditDirEnvVar, "")
	t.Setenv(xdgStateHomeEnvVar, "")
	t.Setenv("HOME", "/Users/alice")

	got, err := SecureActivityLogPath("run-abc")
	if err != nil {
		t.Fatalf("SecureActivityLogPath: %v", err)
	}
	want := filepath.Join("/Users/alice", ".local", "state", "tracker", "runs", "run-abc", "activity.jsonl")
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

// TestSecureActivityLogPath_EmptyRunID rejects empty runID — defensive
// guard so a caller that forgot to populate runID gets a loud error
// instead of a path under the base dir's root.
func TestSecureActivityLogPath_EmptyRunID(t *testing.T) {
	_, err := SecureActivityLogPath("")
	if err == nil {
		t.Fatal("expected error for empty runID")
	}
	if !strings.Contains(err.Error(), "empty runID") {
		t.Errorf("error = %q, want it to mention empty runID", err)
	}
}

// TestSecureActivityLogPath_RejectsTraversal pins runID validation:
// a tampered checkpoint must not be able to escape the secure base
// via path-traversal forms in the run_id field.
func TestSecureActivityLogPath_RejectsTraversal(t *testing.T) {
	t.Setenv(auditDirEnvVar, "/tmp/audit-base")
	cases := []string{
		"..",
		".",
		"../escape",
		"foo/bar",
		`foo\bar`,
		"../../etc/passwd",
		"./local",
	}
	for _, runID := range cases {
		t.Run(runID, func(t *testing.T) {
			if _, err := SecureActivityLogPath(runID); err == nil {
				t.Errorf("SecureActivityLogPath(%q) accepted; want rejection", runID)
			}
		})
	}
}

// TestSecureActivityLogPath_AcceptsCleanRunIDs pins the positive
// shape: random hex (the default generateRunID format) and other
// clean single-element strings are accepted.
func TestSecureActivityLogPath_AcceptsCleanRunIDs(t *testing.T) {
	t.Setenv(auditDirEnvVar, "/tmp/audit-base")
	cases := []string{
		"abc123",
		"deadbeef0123",
		"run-1",
		"2026-05-13T10-00-00",
	}
	for _, runID := range cases {
		t.Run(runID, func(t *testing.T) {
			if _, err := SecureActivityLogPath(runID); err != nil {
				t.Errorf("SecureActivityLogPath(%q) rejected: %v", runID, err)
			}
		})
	}
}

// TestSecureActivityLogBase_IgnoresRelativeEnv pins that a relative
// TRACKER_AUDIT_DIR / XDG_STATE_HOME silently falls through to the
// next candidate. The threat is that a misconfigured value like
// "TRACKER_AUDIT_DIR=.tracker/runs" would land the "secure" log
// inside the process CWD — defeating the relocation defense. We
// prefer silent fallback over erroring because the only consumer of
// errors here is HandlePipelineEvent which swallows them — erroring
// would silently drop all events instead of falling back to a usable
// default location.
func TestSecureActivityLogBase_IgnoresRelativeEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("HOME-default branch is unix-only; Windows uses LOCALAPPDATA")
	}
	t.Setenv(auditDirEnvVar, "relative/path")
	t.Setenv(xdgStateHomeEnvVar, ".local/state")
	t.Setenv("HOME", "/Users/alice")

	got, err := SecureActivityLogPath("run-abc")
	if err != nil {
		t.Fatalf("SecureActivityLogPath: %v", err)
	}
	// Both relative env vars must be ignored; HOME default wins.
	want := filepath.Join("/Users/alice", ".local", "state", "tracker", "runs", "run-abc", "activity.jsonl")
	if got != want {
		t.Errorf("path = %q, want %q (relative env vars must be ignored)", got, want)
	}
}

// TestSecureTurnCheckpointPath pins that the #427 turn snapshot lands under the
// SAME secure run dir as the activity log and checkpoint, keyed by node id.
func TestSecureTurnCheckpointPath(t *testing.T) {
	t.Setenv(auditDirEnvVar, "/tmp/custom-audit")
	got, err := SecureTurnCheckpointPath("run-abc", "Build")
	if err != nil {
		t.Fatalf("SecureTurnCheckpointPath: %v", err)
	}
	want := filepath.Join("/tmp/custom-audit", "run-abc", "turns", "Build.json")
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

// TestSecureTurnCheckpointPath_RejectsUnsafeIDs pins that a run id or node id
// that could escape the secure base is refused, not silently sanitized.
func TestSecureTurnCheckpointPath_RejectsUnsafeIDs(t *testing.T) {
	t.Setenv(auditDirEnvVar, "/tmp/custom-audit")
	cases := []struct{ run, node string }{
		{"../escape", "Build"},
		{"run-abc", "../etc/passwd"},
		{"run-abc", "a/b"},
		{"run-abc", ""},
		{"run-abc", "."},
	}
	for _, c := range cases {
		if _, err := SecureTurnCheckpointPath(c.run, c.node); err == nil {
			t.Errorf("SecureTurnCheckpointPath(%q,%q) = nil error, want rejection", c.run, c.node)
		}
	}
}

// TestActivityLogSentinel pins the exact byte sequence. Changing it is
// a wire-format break that requires a migration plan.
func TestActivityLogSentinel(t *testing.T) {
	if len(ActivityLogSentinel) != 2 {
		t.Fatalf("sentinel length = %d, want 2", len(ActivityLogSentinel))
	}
	if ActivityLogSentinel[0] != 0x1f {
		t.Errorf("sentinel[0] = 0x%02x, want 0x1f (Unit Separator)", ActivityLogSentinel[0])
	}
	if ActivityLogSentinel[1] != 0x1e {
		t.Errorf("sentinel[1] = 0x%02x, want 0x1e (Record Separator)", ActivityLogSentinel[1])
	}
}
