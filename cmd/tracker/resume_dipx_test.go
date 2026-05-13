// ABOUTME: Tests for resume-time .dipx bundle identity verification.
// ABOUTME: Covers match, mismatch, downgrade, upgrade, and force-override cases.
package main

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/dippin-lang/dipx"
	"github.com/2389-research/tracker/internal/dipxtest"
	"github.com/2389-research/tracker/pipeline"
)

func TestVerifyResumeBundle_MatchesIdentity(t *testing.T) {
	err := verifyResumeBundle(
		"sha256:"+strings.Repeat("a", 64),
		"sha256:"+strings.Repeat("a", 64),
		false,
	)
	if err != nil {
		t.Errorf("matching identities should pass: %v", err)
	}
}

func TestVerifyResumeBundle_MismatchAbortsByDefault(t *testing.T) {
	err := verifyResumeBundle(
		"sha256:"+strings.Repeat("a", 64),
		"sha256:"+strings.Repeat("b", 64),
		false,
	)
	if err == nil {
		t.Fatal("expected error on identity mismatch, got nil")
	}
	if !errors.Is(err, errBundleIdentityMismatch) {
		t.Errorf("expected errBundleIdentityMismatch, got %v", err)
	}
	if !strings.Contains(err.Error(), "force-bundle-mismatch") {
		t.Errorf("error should mention --force-bundle-mismatch: %v", err)
	}
}

func TestVerifyResumeBundle_MismatchAllowedWithForce(t *testing.T) {
	err := verifyResumeBundle(
		"sha256:"+strings.Repeat("a", 64),
		"sha256:"+strings.Repeat("b", 64),
		true,
	)
	if err != nil {
		t.Errorf("--force-bundle-mismatch should allow mismatch: %v", err)
	}
}

func TestVerifyResumeBundle_DowngradeRejected(t *testing.T) {
	err := verifyResumeBundle("sha256:"+strings.Repeat("a", 64), "", false)
	if err == nil {
		t.Error("expected downgrade rejection")
	}
}

func TestVerifyResumeBundle_UpgradeRejected(t *testing.T) {
	err := verifyResumeBundle("", "sha256:"+strings.Repeat("a", 64), false)
	if err == nil {
		t.Error("expected upgrade rejection")
	}
}

func TestVerifyResumeBundle_NeitherSideHasIdentity(t *testing.T) {
	err := verifyResumeBundle("", "", false)
	if err != nil {
		t.Errorf("no-identity-either-side should pass unchanged: %v", err)
	}
}

// TestCurrentBundleIdentity_RealDipxBundle exercises the dipx.Open path and
// verifies the returned identity matches the "sha256:<64-hex>" shape.
func TestCurrentBundleIdentity_RealDipxBundle(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "entry.dip")
	if err := os.WriteFile(entry, []byte(dipxtest.MinimalDip("ident_test", "start", "exit")), 0o644); err != nil {
		t.Fatal(err)
	}
	bundlePath := dipxtest.PackTestBundle(t, entry)

	id, err := currentBundleIdentity(bundlePath)
	if err != nil {
		t.Fatalf("currentBundleIdentity: %v", err)
	}
	if !strings.HasPrefix(id, "sha256:") {
		t.Errorf("expected sha256: prefix, got %q", id)
	}
	if len(id) != len("sha256:")+64 {
		t.Errorf("expected len 71 (sha256: + 64 hex), got %d (%q)", len(id), id)
	}
}

// TestCurrentBundleIdentity_NonDipxExtensions verifies that non-.dipx paths
// short-circuit to an empty identity without touching the filesystem.
func TestCurrentBundleIdentity_NonDipxExtensions(t *testing.T) {
	cases := []string{"foo.dip", "foo.dot", "foo", "foo.txt"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			id, err := currentBundleIdentity(name)
			if err != nil {
				t.Errorf("expected nil err for %q, got %v", name, err)
			}
			if id != "" {
				t.Errorf("expected empty identity for %q, got %q", name, id)
			}
		})
	}
}

// TestCurrentBundleIdentity_MissingDipxFile verifies the dipx.Open error is
// wrapped with the "resume verification" prefix so operators can trace it.
func TestCurrentBundleIdentity_MissingDipxFile(t *testing.T) {
	id, err := currentBundleIdentity(filepath.Join(t.TempDir(), "missing.dipx"))
	if err == nil {
		t.Fatal("expected error for missing .dipx, got nil")
	}
	if !strings.Contains(err.Error(), "resume verification") {
		t.Errorf("error should be wrapped with 'resume verification', got: %v", err)
	}
	if id != "" {
		t.Errorf("expected empty id on error, got %q", id)
	}
}

// writeFakeCheckpoint writes a minimal checkpoint.json under
// <workdir>/.tracker/runs/<runID>/ with the given stored bundle identity
// so resolveRunCheckpoint can be exercised end-to-end against the test's
// temporary workdir.
func writeFakeCheckpoint(t *testing.T, workdir, runID, storedBundleIdentity string) string {
	t.Helper()
	runDir := filepath.Join(workdir, ".tracker", "runs", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}
	cp := &pipeline.Checkpoint{
		RunID:          runID,
		BundleIdentity: storedBundleIdentity,
		Timestamp:      time.Now(),
	}
	cpPath := filepath.Join(runDir, "checkpoint.json")
	if err := pipeline.SaveCheckpoint(cp, cpPath); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	return cpPath
}

// packBundleAndGetIdentity packs a real .dipx bundle and returns (path,
// identity) so tests can write the checkpoint with the matching identity.
func packBundleAndGetIdentity(t *testing.T, workflowName string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	entryPath := filepath.Join(dir, "entry.dip")
	if err := os.WriteFile(entryPath, []byte(dipxtest.MinimalDip(workflowName, "start", "exit")), 0o644); err != nil {
		t.Fatalf("write entry .dip: %v", err)
	}
	bundlePath := dipxtest.PackTestBundle(t, entryPath)

	bundle, err := dipx.Open(context.Background(), bundlePath)
	if err != nil {
		t.Fatalf("dipx.Open: %v", err)
	}
	id := bundle.Identity()
	identity := "sha256:" + hex.EncodeToString(id[:])
	return bundlePath, identity
}

// TestResolveRunCheckpoint_BundleMismatch_Aborts exercises the full
// resolveRunCheckpoint integration: a fake checkpoint pinned to one bundle
// identity, then a resume attempt against a different real .dipx bundle.
// Without --force-bundle-mismatch the resume must abort with
// errBundleIdentityMismatch.
func TestResolveRunCheckpoint_BundleMismatch_Aborts(t *testing.T) {
	workdir := t.TempDir()
	const runID = "resume-mismatch"

	// Checkpoint claims a different bundle identity.
	stored := "sha256:" + strings.Repeat("a", 64)
	writeFakeCheckpoint(t, workdir, runID, stored)

	// Real .dipx with a different content-addressed identity.
	bundlePath, _ := packBundleAndGetIdentity(t, "mismatch_aborts")

	cfg := runConfig{
		workdir:             workdir,
		resumeID:            runID,
		pipelineFile:        bundlePath,
		forceBundleMismatch: false,
	}

	info, err := resolveRunCheckpoint(cfg)
	if err == nil {
		t.Fatal("expected error on identity mismatch, got nil")
	}
	if !errors.Is(err, errBundleIdentityMismatch) {
		t.Errorf("expected errBundleIdentityMismatch, got: %v", err)
	}
	if info.CheckpointPath != "" || info.BundleMismatchForced {
		t.Errorf("expected zero resumeInfo on error, got %+v", info)
	}
}

// TestResolveRunCheckpoint_BundleMismatch_AllowedWithForce verifies that
// --force-bundle-mismatch lets a mismatched resume through and that the
// returned resumeInfo carries the override flag plus both identities so
// the caller can record the audit entry.
func TestResolveRunCheckpoint_BundleMismatch_AllowedWithForce(t *testing.T) {
	workdir := t.TempDir()
	const runID = "resume-force"

	stored := "sha256:" + strings.Repeat("a", 64)
	writeFakeCheckpoint(t, workdir, runID, stored)

	bundlePath, currentID := packBundleAndGetIdentity(t, "mismatch_forced")

	// resolveRunCheckpoint prints a forced-mismatch warning to stderr; the
	// warning is informational and visible in test output. We assert on the
	// returned resumeInfo, not on stderr — the unit-level verifyResumeBundle
	// tests already exercise the error/warning string content directly.

	cfg := runConfig{
		workdir:             workdir,
		resumeID:            runID,
		pipelineFile:        bundlePath,
		forceBundleMismatch: true,
	}

	info, err := resolveRunCheckpoint(cfg)
	if err != nil {
		t.Fatalf("--force-bundle-mismatch should allow resume: %v", err)
	}
	if !info.BundleMismatchForced {
		t.Error("BundleMismatchForced = false; want true after force-override")
	}
	if info.OriginalIdentity != stored {
		t.Errorf("OriginalIdentity = %q, want %q", info.OriginalIdentity, stored)
	}
	if info.CurrentIdentity != currentID {
		t.Errorf("CurrentIdentity = %q, want %q", info.CurrentIdentity, currentID)
	}
	if info.RunID != runID {
		t.Errorf("RunID = %q, want %q", info.RunID, runID)
	}
	if info.CheckpointPath == "" {
		t.Error("CheckpointPath should be populated on success")
	}
}

// TestResolveRunCheckpoint_BundleMatch verifies the happy path: when the
// checkpoint's stored identity matches the current bundle's identity, the
// resume proceeds without force and BundleMismatchForced stays false.
func TestResolveRunCheckpoint_BundleMatch(t *testing.T) {
	workdir := t.TempDir()
	const runID = "resume-match"

	bundlePath, bundleID := packBundleAndGetIdentity(t, "mismatch_match")

	// Checkpoint records the *real* bundle identity so the resume should
	// proceed cleanly.
	writeFakeCheckpoint(t, workdir, runID, bundleID)

	cfg := runConfig{
		workdir:             workdir,
		resumeID:            runID,
		pipelineFile:        bundlePath,
		forceBundleMismatch: false,
	}

	info, err := resolveRunCheckpoint(cfg)
	if err != nil {
		t.Fatalf("matching identities should resume cleanly: %v", err)
	}
	if info.BundleMismatchForced {
		t.Error("BundleMismatchForced = true on a clean match; want false")
	}
	if info.OriginalIdentity != bundleID || info.CurrentIdentity != bundleID {
		t.Errorf("identities should both equal %q, got original=%q current=%q",
			bundleID, info.OriginalIdentity, info.CurrentIdentity)
	}
	if info.CheckpointPath == "" {
		t.Error("CheckpointPath should be populated on success")
	}
	if info.RunID != runID {
		t.Errorf("RunID = %q, want %q", info.RunID, runID)
	}
}

// TestResolveRunCheckpoint_NewRun verifies the non-resume path: with no
// resumeID, resolveRunCheckpoint returns a zero-valued resumeInfo without
// touching the filesystem.
func TestResolveRunCheckpoint_NewRun(t *testing.T) {
	cfg := runConfig{
		workdir:      t.TempDir(),
		resumeID:     "",
		pipelineFile: "doesnt-matter.dip",
	}
	info, err := resolveRunCheckpoint(cfg)
	if err != nil {
		t.Fatalf("new run should not error: %v", err)
	}
	if info != (resumeInfo{}) {
		t.Errorf("new run should return zero resumeInfo, got %+v", info)
	}
}
