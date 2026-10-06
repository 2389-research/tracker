//go:build windows

// ABOUTME: Windows process-liveness stub for ownership-aware container cleanup (#598).
package main

// processAlive reports whether pid names a live process on this host. Windows has
// no signal-0 existence probe, and the swebench harness is not supported on
// Windows (it drives Docker through a POSIX shell), so this is a build-time stub.
//
// It unconditionally reports the process as alive. There is no Windows API that
// positively proves a PID is dead without the right to open its handle:
// os.FindProcess opens the process and fails identically for a gone PID and for a
// live PID owned by another user (access denied), so it cannot distinguish the
// two. Because cleanup only ever removes containers whose owner is NOT alive,
// always answering "alive" means the Windows build never deletes a container it
// cannot prove is orphaned — in particular it never reaps another user's live
// containers. The trade-off, that a genuinely dead PID also reads as alive, is
// acceptable: the harness does not run on Windows, so leaning toward "never
// delete" costs nothing here.
func processAlive(pid int) bool {
	return true
}
