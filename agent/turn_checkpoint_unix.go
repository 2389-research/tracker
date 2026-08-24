// ABOUTME: Per-OS open flag for the durable turn-checkpoint atomic write.
//go:build unix

package agent

import "syscall"

// turnCheckpointNoFollow refuses to traverse a symlink at the turn-snapshot
// temp-write destination. Mirrors pipeline.snapshotNoFollow (#559): the snapshot
// may sit in a run's secure state dir, and O_NOFOLLOW on the temp write defeats a
// TOCTOU where a symlink is planted at <path>.tmp to redirect the write.
const turnCheckpointNoFollow = syscall.O_NOFOLLOW
