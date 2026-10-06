//go:build windows

// ABOUTME: Pins the conservative "assume alive" contract of the Windows liveness stub (#598, #661).
package main

import "testing"

// TestProcessAlive_WindowsAlwaysAlive documents and pins the conservative stub
// contract: on Windows a PID can never be positively proven dead (os.FindProcess
// fails identically for a gone PID and for another user's access-denied live
// PID), so processAlive always reports alive. Cleanup removes only containers
// whose owner is NOT alive, so this guarantees the Windows build never reaps a
// container it cannot prove is orphaned — including another user's live ones.
func TestProcessAlive_WindowsAlwaysAlive(t *testing.T) {
	// The current process is obviously alive.
	if !processAlive(1) {
		t.Error("processAlive(1) must report alive on Windows")
	}
	// Even an almost-certainly-dead PID must read as alive: the stub leans
	// toward "never delete" rather than risk reaping a live container it merely
	// lacks the rights to probe.
	if !processAlive(1 << 30) {
		t.Error("an unprovable PID must still be reported alive on Windows")
	}
}
