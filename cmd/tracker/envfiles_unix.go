//go:build !windows

// ABOUTME: Unix open for .env files: O_NOFOLLOW so a symlink planted at <workdir>/.env is refused at the syscall.
// ABOUTME: The Lstat check in readEnvFileSecurely gives the friendly notice; this closes the race behind it.
package main

import (
	"os"
	"syscall"
)

func openEnvFileNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}
