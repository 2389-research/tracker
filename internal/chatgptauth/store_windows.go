//go:build windows

// ABOUTME: Windows safety check for the ChatGPT token file: Lstat symlink check only, matching
// ABOUTME: cmd/tracker/envfiles_windows.go — mode bits carry no meaning on this platform.
package chatgptauth

import "os"

// checkFileSafe refuses to read path only when it is a symlink; Windows mode
// bits aren't checked, matching the config .env loader's Windows behavior.
func checkFileSafe(path string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return &UnsafeFileError{Path: path, Problem: "it is a symlink"}
	}
	return nil
}

// openNoFollow opens path for reading. Windows has no O_NOFOLLOW; the Lstat
// check in checkFileSafe above is the only guard, as in envfiles_windows.go.
func openNoFollow(path string) (*os.File, error) {
	return os.Open(path)
}
