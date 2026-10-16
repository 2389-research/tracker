//go:build !windows

// ABOUTME: Unix safety check and no-follow open for the ChatGPT token file: refuses a symlink or
// ABOUTME: a group/other-accessible mode (mirrors cmd/tracker/envfiles_unix.go's O_NOFOLLOW + Lstat pattern).
package chatgptauth

import (
	"fmt"
	"os"
	"syscall"
)

// checkFileSafe refuses to read path when Lstat shows it is a symlink, or
// when its mode bits grant group or other any access. This is the Unix half
// of Load's guard; store_windows.go provides the symlink-only counterpart.
func checkFileSafe(path string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return &UnsafeFileError{Path: path, Problem: "it is a symlink"}
	}
	if info.Mode().Perm()&0o077 != 0 {
		return &UnsafeFileError{
			Path:    path,
			Problem: fmt.Sprintf("mode %04o allows group or other access", info.Mode().Perm()),
		}
	}
	return nil
}

// openNoFollow opens path for reading with O_NOFOLLOW, closing the race
// between the Lstat safety check above and the open itself (the file could
// be replaced by a symlink in between).
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}
