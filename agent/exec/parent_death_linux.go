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
// OS thread. Pairs with applyParentDeathSig: PR_SET_PDEATHSIG is thread-
// scoped, so the kernel sends SIGKILL when the spawning thread terminates,
// not the parent process. If the Go runtime migrates the calling goroutine
// off its original thread and that thread is later retired (during GC
// stop-the-world or thread-pool reduction), every child whose Pdeathsig was
// armed on that thread gets a premature kill (#272 review,
// coderabbitai parent_death_linux.go:43).
//
// Locking the goroutine for the duration of cmd.Run() pins the thread alive
// — Go won't retire a thread that has a goroutine locked to it. Callers
// should defer the returned unlock immediately:
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
