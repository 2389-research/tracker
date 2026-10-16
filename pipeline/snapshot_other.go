// ABOUTME: Non-unix stub for snapshotNoFollow — Windows lacks O_NOFOLLOW.
//go:build !unix

package pipeline

// snapshotNoFollow is a no-op on platforms that lack O_NOFOLLOW.
// Windows handles symlinks differently and the relative-path threat
// model differs from POSIX; the bundle snapshot is best-effort here.
const snapshotNoFollow = 0

// untrustedReadNonBlock is a no-op where O_NONBLOCK is unavailable;
// LoadCheckpointBounded still refuses a non-regular file after the open.
const untrustedReadNonBlock = 0
