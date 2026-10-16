// ABOUTME: SkipUnlessPermissionsEnforced skips a test that needs the OS to refuse access by mode bits.
// ABOUTME: One probe per test binary: can this process create a file inside a 0500 directory?

package testutil

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// permissionsNotEnforced is the reason SkipUnlessPermissionsEnforced skips with.
const permissionsNotEnforced = "permission bits are not enforced for this process " +
	"(for example root or CAP_DAC_OVERRIDE): it created a file inside a 0500 directory, " +
	"so this test cannot check a permission failure"

// probePermissions runs modeBitsEnforced once per test binary in
// os.TempDir(), where t.TempDir() puts test directories too, and caches the
// answer. It is a variable so tests can stand in a result.
var probePermissions = sync.OnceValues(func() (bool, error) {
	return modeBitsEnforced(os.TempDir())
})

// PermissionsEnforced reports whether the OS refuses this process access that
// file mode bits deny. Root, a process holding CAP_DAC_OVERRIDE, or a
// filesystem that ignores mode bits lets a write into a 0500 directory
// through, so a test that expects EACCES from a chmod-ed path fails there
// although the code under test is fine. A probe that cannot run fails the
// test instead of guessing.
func PermissionsEnforced(t testing.TB) bool {
	t.Helper()
	enforced, err := probePermissions()
	if err != nil {
		t.Fatalf("probe whether permission bits are enforced: %v", err)
	}
	return enforced
}

// SkipUnlessPermissionsEnforced skips the test when PermissionsEnforced
// reports false. Call it at the start of a test, or of the subtest, whose
// assertion needs the OS to refuse access because of file mode bits.
func SkipUnlessPermissionsEnforced(t testing.TB) {
	t.Helper()
	if !PermissionsEnforced(t) {
		t.Skip(permissionsNotEnforced)
	}
}

// modeBitsEnforced creates a 0500 directory under parent, tries to create a
// file inside it, and removes both. Only a create that succeeds counts as
// "not enforced", so no test skips unless the probe saw the bypass happen.
// Failing to set up or clean up is returned as an error.
func modeBitsEnforced(parent string) (enforced bool, err error) {
	dir, err := os.MkdirTemp(parent, "permissions-probe-")
	if err != nil {
		return false, err
	}
	defer func() {
		if rmErr := os.RemoveAll(dir); err == nil {
			err = rmErr
		}
	}()
	if err = os.Chmod(dir, 0o500); err != nil {
		return false, err
	}
	createErr := os.WriteFile(filepath.Join(dir, "probe"), nil, 0o600)
	if err = os.Chmod(dir, 0o700); err != nil {
		return false, err
	}
	return createErr != nil, nil
}
