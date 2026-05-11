// ABOUTME: Tests for resume-time .dipx bundle identity verification.
// ABOUTME: Covers match, mismatch, downgrade, upgrade, and force-override cases.
package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/internal/dipxtest"
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
