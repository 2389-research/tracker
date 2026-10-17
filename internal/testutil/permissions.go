// ABOUTME: SkipUnlessPermissionsEnforced skips a test that needs the OS to refuse access by mode bits.
// ABOUTME: One probe per test binary: does the OS refuse this process a write into a 0500 dir and a lookup in a 0600 dir?

package testutil

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// permissionsNotEnforced is the reason SkipUnlessPermissionsEnforced skips with.
const permissionsNotEnforced = "permission bits are not enforced for this process " +
	"(for example root, CAP_DAC_OVERRIDE or CAP_DAC_READ_SEARCH): it wrote into a 0500 " +
	"directory or looked up a file in a directory without search permission, " +
	"so this test cannot check a permission failure"

// probePermissions runs modeBitsEnforced once per test binary in
// os.TempDir(), where t.TempDir() puts test directories too, and caches the
// answer. It is a variable so tests can stand in a result.
var probePermissions = sync.OnceValues(func() (bool, error) {
	return modeBitsEnforced(os.TempDir())
})

// PermissionsEnforced reports whether the OS refuses this process access that
// file mode bits deny. Root, a process holding CAP_DAC_OVERRIDE or
// CAP_DAC_READ_SEARCH, or a filesystem that ignores mode bits lets a write
// into a 0500 directory or a lookup in a 0600 one through, so a test that
// expects EACCES from a chmod-ed path fails there although the code under
// test is fine. A probe that cannot run fails the test instead of guessing.
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

// probeCreate creates the probe's file; tests stand in failures.
var probeCreate = func(name string) error { return os.WriteFile(name, nil, 0o600) }

// modeBitsEnforced tries, under parent, to create a file inside a 0500
// directory and to look up a file inside a 0600 one, which lacks the search
// bit, and removes both directories. Mode bits count as enforced only when
// both attempts fail with a permission error; one that succeeds means this
// process gets through, so no test skips unless the probe saw that happen.
// Any other failure, such as a full disk, is returned as an error rather than
// a verdict, and so is failing to set up or clean up.
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
	writeErr, err := tryWrite(filepath.Join(dir, "no-write"))
	if err != nil {
		return false, err
	}
	lookupErr, err := tryLookup(filepath.Join(dir, "no-search"))
	if err != nil {
		return false, err
	}
	return permissionVerdict(writeErr, lookupErr)
}

// tryWrite makes path a 0500 directory, tries to create a file inside it and
// gives the directory its write bit back. It returns the attempt's error, and
// err when the directory cannot be set up or restored.
func tryWrite(path string) (attempt, err error) {
	if err := os.Mkdir(path, 0o500); err != nil {
		return nil, err
	}
	attempt = probeCreate(filepath.Join(path, "probe"))
	return attempt, os.Chmod(path, 0o700)
}

// tryLookup makes path a directory holding a file, takes away the
// directory's search bit, tries to stat the file and gives the bit back. It
// returns the attempt's error, and err when the directory cannot be set up or
// restored.
func tryLookup(path string) (attempt, err error) {
	if err := os.Mkdir(path, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(path, "probe"), nil, 0o600); err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, err
	}
	_, attempt = os.Stat(filepath.Join(path, "probe"))
	return attempt, os.Chmod(path, 0o700)
}

// permissionVerdict reads the attempts. One that failed for another reason
// than permission is returned as the error, whatever the others did; then
// any that succeeded means mode bits are not enforced, and all refused for
// permission means enforced.
func permissionVerdict(attempts ...error) (bool, error) {
	enforced := true
	for _, attempt := range attempts {
		switch {
		case attempt == nil:
			enforced = false
		case !errors.Is(attempt, fs.ErrPermission):
			return false, attempt
		}
	}
	return enforced, nil
}
