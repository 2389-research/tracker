// ABOUTME: Tests for resume-time .dipx bundle identity verification.
// ABOUTME: Covers match, mismatch, downgrade, upgrade, and force-override cases.
package main

import (
	"errors"
	"strings"
	"testing"
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
