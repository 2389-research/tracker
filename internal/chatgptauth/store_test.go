// ABOUTME: Tests for the ChatGPT token file: default path resolution, atomic save, and the
// ABOUTME: Load-time guards against a missing, incomplete, symlinked or over-permissive file.
package chatgptauth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDefaultPathUsesXDG(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath() unexpected error: %v", err)
	}
	want := filepath.Join(xdg, "tracker", FileName)
	if got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestSaveWritesPrivateFileAtomically(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub")
	path := filepath.Join(dir, FileName)
	want := Tokens{
		IDToken:      "id-token",
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		AccountID:    "acct-1",
		LastRefresh:  time.Unix(1760000000, 0).UTC(),
	}

	if err := Save(path, want); err != nil {
		t.Fatalf("Save() unexpected error: %v", err)
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat saved file: %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %04o, want 0600", perm)
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat saved dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("dir mode = %04o, want 0700", perm)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read saved dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != FileName {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("dir entries = %v, want exactly [%s] (no leftover temp file)", names, FileName)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if got.IDToken != want.IDToken ||
		got.AccessToken != want.AccessToken ||
		got.RefreshToken != want.RefreshToken ||
		got.AccountID != want.AccountID ||
		!got.LastRefresh.Equal(want.LastRefresh) {
		t.Errorf("Load() round-trip = %+v, want %+v", got, want)
	}
}

func TestLoadMissingIsNotSignedIn(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)

	_, err := Load(path)
	if !errors.Is(err, ErrNotSignedIn) {
		t.Errorf("Load() error = %v, want ErrNotSignedIn", err)
	}
}

// TestLoadRefusesSymlink and TestLoadRefusesGroupReadable are Unix-only:
// symlink mode bits and permission bits don't carry the same meaning on
// Windows (store_windows.go checks only Lstat's symlink bit). A runtime skip
// keeps both in this one store_test.go file alongside the cross-platform
// tests above, rather than splitting into a second, unlisted test file.
func TestLoadRefusesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink mode bits aren't checked on Windows; see store_windows.go")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "real-"+FileName)
	writeRawTokenFile(t, target, 0o600, Tokens{})

	link := filepath.Join(dir, FileName)
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink() unexpected error: %v", err)
	}

	_, err := Load(link)
	var unsafeErr *UnsafeFileError
	if !errors.As(err, &unsafeErr) {
		t.Fatalf("Load() error = %v, want *UnsafeFileError", err)
	}
}

func TestLoadRefusesGroupReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits carry no meaning on Windows; see store_windows.go")
	}
	path := filepath.Join(t.TempDir(), FileName)
	writeRawTokenFile(t, path, 0o640, Tokens{})

	_, err := Load(path)
	var unsafeErr *UnsafeFileError
	if !errors.As(err, &unsafeErr) {
		t.Fatalf("Load() error = %v, want *UnsafeFileError", err)
	}
	if !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("error %q does not contain %q", err.Error(), "chmod 600")
	}
}

func TestLoadRejectsIncompleteTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	const secretAccessToken = "super-secret-access-token"
	writeRawTokenFile(t, path, 0o600, Tokens{
		AccessToken:  secretAccessToken,
		RefreshToken: "refresh-token",
		AccountID:    "", // missing — this is what must trip the "sign in again" error
	})

	_, err := Load(path)
	if err == nil {
		t.Fatalf("Load() = nil error, want an error (incomplete tokens)")
	}
	if !strings.Contains(err.Error(), "sign in again") {
		t.Errorf("error %q does not contain %q", err.Error(), "sign in again")
	}
	if strings.Contains(err.Error(), secretAccessToken) {
		t.Errorf("error %q leaks the access token value", err.Error())
	}
}

// writeRawTokenFile writes tok directly at the given mode, bypassing Save
// (which always writes 0600), so tests can construct an unsafe or
// intentionally incomplete file on disk.
func writeRawTokenFile(t *testing.T, path string, perm os.FileMode, tok Tokens) {
	t.Helper()
	data, err := json.Marshal(tok)
	if err != nil {
		t.Fatalf("marshal tokens: %v", err)
	}
	if err := os.WriteFile(path, data, perm); err != nil {
		t.Fatalf("write raw token file: %v", err)
	}
}
