// ABOUTME: Linux-specific defense: PR_SET_PDEATHSIG=SIGKILL on every child.
// ABOUTME: Ensures spawned subprocesses die when the tracker parent dies.

//go:build linux

package exec

import (
	"os/exec"
	"runtime"
	"syscall"
)

// pinCallingThreadForParentDeath locks the calling goroutine to its current
// OS thread for the lifetime of the spawn. Defensive measure paired with
// applyParentDeathSig.
//
// PDEATHSIG semantics, accurately: modern Linux delivers PR_SET_PDEATHSIG on
// **parent process** exit, not on individual parent-thread exit. The
// earlier coderabbitai review (#272 round 2, parent_death_linux.go:43)
// cited a web result claiming the signal is thread-scoped — that result was
// outdated and contradicted by current kernel behavior (#275 review,
// Copilot parent_death_linux.go:30). For the modern semantics the lock is
// not strictly required for correctness.
//
// We keep it as a conservative measure for three reasons:
//  1. Defensive depth on the off chance a child runs against a kernel where
//     the older thread-scoped behavior still applies (no documented support
//     window for tracker says "kernel >= 2.6.27 only").
//  2. Pinning the goroutine guarantees the fork-exec syscall and the
//     surrounding cmd.Run loop observe a stable thread identity, which makes
//     reasoning about any future per-thread state simpler.
//  3. The cost is one goroutine pinned for the duration of cmd.Run, which
//     tracker invokes serially per agent. No measurable scheduling impact.
//
// Callers should defer the returned unlock immediately:
//
//	unlock := pinCallingThreadForParentDeath()
//	defer unlock()
//
// On non-Linux builds the helper is a no-op (see parent_death_other.go).
func pinCallingThreadForParentDeath() func() {
	runtime.LockOSThread()
	return runtime.UnlockOSThread
}

// applyParentDeathSig configures cmd's SysProcAttr so the kernel sends SIGKILL
// to the child when its immediate parent (this process) dies. Protects against
// orphan accumulation when:
//
//   - A test binary panics or is SIGKILL'd before reaping its children.
//   - A tracker run is interrupted mid-flight.
//   - An external watcher kicks off `go test` repeatedly and stale runs leave
//     children behind.
//
// Without this, orphaned children get reparented to init and continue
// consuming CPU until they exit on their own. Under load they accumulate
// faster than they complete, fork-bombing the host (observed during #272 dev:
// 132 live __jail-exec orphans, load avg 74 on a 4-core box).
//
// Combined with Setpgid (which already lives on each LocalEnvironment.Exec*
// path), this gives layered defense:
//   - Setpgid + cmd.Cancel kill the process group on timeout/cancel.
//   - Pdeathsig kills the immediate child when the parent dies (any cause).
//   - Together they cover the "subprocess survives parent termination" hole.
//
// Pdeathsig persists across execve, so it survives RunJailExec's syscall.Exec
// into `sh -c <cmd>` — the agent's bash and all descendants inherit it.
//
// Linux-only; macOS provides no equivalent through Go's syscall package.
// The cross-platform helper is in parent_death_other.go.
func applyParentDeathSig(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}
