// ABOUTME: Tests for the sandbox device-node hygiene preflight (#423).
// ABOUTME: Uses a stubbed device probe so the real host /dev/null is never touched.
package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// TestCheckDeviceNodes_StubBroken verifies a broken-device probe surfaces a
// SPECIFIC diagnostic wrapping ErrDeviceNodeUnusable, mentioning the device and
// actionable remediation — not a generic deep git error.
func TestCheckDeviceNodes_StubBroken(t *testing.T) {
	err := checkDeviceNodes(func() error { return errors.New("EBADF /dev/null") })
	if err == nil {
		t.Fatal("expected error from broken device probe, got nil")
	}
	if !errors.Is(err, ErrDeviceNodeUnusable) {
		t.Fatalf("expected ErrDeviceNodeUnusable, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "/dev/null") {
		t.Errorf("diagnostic missing device path: %q", msg)
	}
	if !strings.Contains(msg, "mknod") {
		t.Errorf("diagnostic missing remediation hint (mknod): %q", msg)
	}
}

// TestCheckDeviceNodes_StubHealthy verifies a healthy probe returns nil — no
// new failure on the happy path (AC3).
func TestCheckDeviceNodes_StubHealthy(t *testing.T) {
	if err := checkDeviceNodes(func() error { return nil }); err != nil {
		t.Fatalf("expected nil for healthy device, got %v", err)
	}
}

// TestApplyGitPreflight_DeviceCheckedFirst proves the device probe runs BEFORE
// any git-dependent probe: with a broken device stub and a requires:git workflow
// in a non-repo dir, the returned error is the device diagnostic, NOT a git
// not-a-repo error. This is AC1's "before the first git-dependent node".
func TestApplyGitPreflight_DeviceCheckedFirst(t *testing.T) {
	orig := defaultDeviceProbe
	defaultDeviceProbe = func() error { return errors.New("simulated broken /dev/null") }
	t.Cleanup(func() { defaultDeviceProbe = orig })

	g := pipeline.NewGraph("device_first_test")
	g.Attrs["requires"] = "git"
	// Force the git check on too, so a passing device probe would otherwise
	// reach the git not-a-repo failure.
	activeGitConfig.policy = string(pipeline.GitPreflightRequire)
	t.Cleanup(func() { activeGitConfig.policy = "" })

	tmp := t.TempDir() // NOT a git repo
	err := applyGitPreflight(context.Background(), g, tmp)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrDeviceNodeUnusable) {
		t.Fatalf("expected device error to be returned before git probe, got %v", err)
	}
	if errors.Is(err, pipeline.ErrGitWorkdirNotRepo) {
		t.Fatalf("git probe ran before device check — device check is not first: %v", err)
	}
}
