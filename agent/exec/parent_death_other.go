// ABOUTME: Non-Linux passthrough for the Pdeathsig orphan defense.

//go:build !linux && !windows

package exec

import "os/exec"

// applyParentDeathSig is a no-op on non-Linux platforms. macOS provides no
// PR_SET_PDEATHSIG equivalent through Go's syscall package; orphaned children
// there must be handled via the existing Setpgid + cmd.Cancel pattern.
// See parent_death_linux.go for the rationale.
func applyParentDeathSig(cmd *exec.Cmd) {}

// pinCallingThreadForParentDeath is a no-op on non-Linux platforms.
// See parent_death_linux.go for the rationale.
func pinCallingThreadForParentDeath() func() { return func() {} }
