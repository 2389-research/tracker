// ABOUTME: Tests for the permission-bits probe behind SkipUnlessPermissionsEnforced.
// ABOUTME: The probe must agree with a direct write into a 0500 directory and leave nothing behind.

package testutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// The probe's verdict must match the operations it stands for, done here
// independently: a write into a 0500 directory and a lookup inside a
// directory without the search bit. An inverted verdict fails as root and as
// a plain user.
func TestModeBitsEnforced_AgreesWithAWriteAndALookup(t *testing.T) {
	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	writeErr := os.WriteFile(filepath.Join(locked, "probe"), nil, 0o600)

	hidden := filepath.Join(t.TempDir(), "hidden")
	if err := os.Mkdir(hidden, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hidden, "probe"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(hidden, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(hidden, 0o700) })
	_, lookupErr := os.Stat(filepath.Join(hidden, "probe"))

	enforced, err := modeBitsEnforced(t.TempDir())
	if err != nil {
		t.Fatalf("modeBitsEnforced: %v", err)
	}
	if want := writeErr != nil && lookupErr != nil; enforced != want {
		t.Errorf("modeBitsEnforced = %v, but a direct write returned %v and a direct lookup %v", enforced, writeErr, lookupErr)
	}
}

// A write that fails for any reason but permission, such as a full disk,
// says nothing about mode bits, so the probe returns it instead of a verdict.
func TestModeBitsEnforced_ReturnsAnUnexpectedWriteError(t *testing.T) {
	orig := probeCreate
	probeCreate = func(string) error { return syscall.ENOSPC }
	t.Cleanup(func() { probeCreate = orig })
	if _, err := modeBitsEnforced(t.TempDir()); !errors.Is(err, syscall.ENOSPC) {
		t.Errorf("modeBitsEnforced error = %v, want one wrapping ENOSPC", err)
	}
}

// An unexpected failure in any attempt is reported, even when another attempt
// got through, so a broken probe never passes for a bypass.
func TestPermissionVerdict_ReportsAnUnexpectedErrorBeforeAVerdict(t *testing.T) {
	for _, attempts := range [][]error{{nil, syscall.EIO}, {syscall.EIO, nil}, {os.ErrPermission, syscall.EIO}} {
		if _, err := permissionVerdict(attempts...); !errors.Is(err, syscall.EIO) {
			t.Errorf("permissionVerdict(%v) error = %v, want EIO", attempts, err)
		}
	}
	if enforced, err := permissionVerdict(os.ErrPermission, nil); err != nil || enforced {
		t.Errorf("permissionVerdict(permission, nil) = %v, %v; want not enforced", enforced, err)
	}
	if enforced, err := permissionVerdict(os.ErrPermission, os.ErrPermission); err != nil || !enforced {
		t.Errorf("permissionVerdict(permission, permission) = %v, %v; want enforced", enforced, err)
	}
}

// The real probe runs in os.TempDir(), so it must not leave its 0500
// directory (or the file it may have created) behind.
func TestModeBitsEnforced_RemovesItsDirectory(t *testing.T) {
	parent := t.TempDir()
	if _, err := modeBitsEnforced(parent); err != nil {
		t.Fatalf("modeBitsEnforced: %v", err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("probe left %d entries in %s: %v", len(entries), parent, entries)
	}
}

// A probe that cannot set itself up returns the error, not a verdict.
func TestModeBitsEnforced_ReportsSetupFailure(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := modeBitsEnforced(missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("modeBitsEnforced(%q) error = %v, want one wrapping os.ErrNotExist", missing, err)
	}
}

// withProbeResult makes the cached probe return enforced and err for one test.
func withProbeResult(t *testing.T, enforced bool, err error) {
	t.Helper()
	orig := probePermissions
	probePermissions = func() (bool, error) { return enforced, err }
	t.Cleanup(func() { probePermissions = orig })
}

func TestSkipUnlessPermissionsEnforced_SkipsWhenBitsAreNotEnforced(t *testing.T) {
	withProbeResult(t, false, nil)
	rec := &recordTB{TB: t}
	SkipUnlessPermissionsEnforced(rec)
	if len(rec.skips) != 1 || !strings.Contains(rec.skips[0], "permission bits are not enforced for this process") {
		t.Errorf("skips = %q, want one saying permission bits are not enforced for this process", rec.skips)
	}
	if len(rec.fatals) != 0 {
		t.Errorf("fatals = %q, want none", rec.fatals)
	}
}

func TestSkipUnlessPermissionsEnforced_RunsWhenBitsAreEnforced(t *testing.T) {
	withProbeResult(t, true, nil)
	rec := &recordTB{TB: t}
	SkipUnlessPermissionsEnforced(rec)
	if len(rec.skips) != 0 || len(rec.fatals) != 0 {
		t.Errorf("skips = %q, fatals = %q; want neither when permission bits are enforced", rec.skips, rec.fatals)
	}
}

func TestPermissionsEnforced_FailsWhenTheProbeCannotRun(t *testing.T) {
	withProbeResult(t, false, errors.New("mkdir: no space left on device"))
	rec := &recordTB{TB: t}
	PermissionsEnforced(rec)
	if len(rec.fatals) != 1 || !strings.Contains(rec.fatals[0], "no space left on device") {
		t.Errorf("fatals = %q, want one carrying the probe error", rec.fatals)
	}
}
