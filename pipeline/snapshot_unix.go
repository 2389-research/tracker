// ABOUTME: Per-OS open-flags for the close-time activity log snapshot.
//go:build unix

package pipeline

import "syscall"

// snapshotNoFollow refuses to traverse a symlink at the snapshot
// destination. Defeats a TOCTOU where a tool subprocess plants a
// symlink at <artifactDir>/<runID>/activity.jsonl pointing somewhere
// else before the runtime's Close-time snapshot fires.
const snapshotNoFollow = syscall.O_NOFOLLOW

// untrustedReadNonBlock opens an untrusted path without blocking, so a FIFO
// planted there cannot hang the open; LoadCheckpointBounded then refuses any
// non-regular file.
const untrustedReadNonBlock = syscall.O_NONBLOCK
