// ABOUTME: Shared error sentinels for the writable_paths fs-jail (issue #272).
// ABOUTME: Used by both Linux jail implementation and non-Linux passthrough stubs.
package exec

import "errors"

// ErrLandlockUnavailable is returned by ProbeLandlock when the host kernel
// doesn't support Landlock ABI v3 (kernel 6.7+), or when the binary is built
// for a non-Linux target. The codergen handler refuses to start a session
// with non-empty WritablePaths on this error.
var ErrLandlockUnavailable = errors.New("landlock ABI v3 not available on this host (requires Linux kernel 6.7+)")

// ErrPathEscape is returned by OpenForWrite when the requested path resolves
// outside the session anchor (via absolute path, parent traversal, or symlink
// escape). The kernel returns EXDEV/ELOOP for openat2 with RESOLVE_BENEATH;
// the helper translates to this sentinel for typed handling upstream.
var ErrPathEscape = errors.New("write path escapes session root")

// ErrPathNotAllowed is returned by OpenForWrite when the resolved path is
// beneath the session anchor but does not match any writable_paths glob.
var ErrPathNotAllowed = errors.New("write path not in writable_paths")
