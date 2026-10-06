//go:build windows

// ABOUTME: Windows process-liveness stub for ownership-aware container cleanup (#598).
package main

import "os"

// processAlive reports whether pid names a live process on this host. Windows has
// no signal-0 existence probe, and the swebench harness is not supported on
// Windows (it drives Docker through a POSIX shell), so this is a build-time stub.
// It conservatively reports the process as alive: cleanup only ever removes
// containers whose owner is NOT alive, so erring toward "alive" means the Windows
// build never deletes a container it cannot positively prove is orphaned.
func processAlive(pid int) bool {
	if _, err := os.FindProcess(pid); err != nil {
		return false
	}
	return true
}
