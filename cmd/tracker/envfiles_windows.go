//go:build windows

// ABOUTME: Windows open for .env files. There is no O_NOFOLLOW; the Lstat symlink check in
// ABOUTME: readEnvFileSecurely is the only guard on this platform.
package main

import "os"

func openEnvFileNoFollow(path string) (*os.File, error) {
	return os.Open(path)
}
