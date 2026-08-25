// ABOUTME: Ownership-aware container cleanup (#598) and deadline/watchdog classification (#608).
// ABOUTME: Keeps the run-identity, cleanup-policy, and watchdog helpers out of docker.go's hot path.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Docker label keys applied to every harness container. `labelSwebench` is
// retained (with the run ID as its value) for back-compat with the pre-#598
// broad `label=swebench` filter; the owner/run/created-at labels drive
// ownership-aware cleanup so a live harness's containers are never removed.
const (
	labelSwebench  = "swebench"            // legacy broad marker; value = run ID
	labelOwner     = "swebench.owner"      // <hostname>:<pid> of the owning harness
	labelRun       = "swebench.run"        // collision-resistant run ID
	labelCreatedAt = "swebench.created-at" // RFC3339 container creation time
)

// defaultWatchdogGrace is the reporting/teardown grace added on top of the
// benchmark deadline before the host watchdog force-kills the agent exec (#608).
// It gives a child that hit its own SWEBENCH_TIMEOUT time to emit its required
// summary line before the container is torn down.
const defaultWatchdogGrace = 60 * time.Second

// newRunID returns a collision-resistant run identity: a second-resolution
// timestamp for human readability plus 4 random bytes so two harnesses started
// in the same second do not share a run label (which would collide container
// names and cleanup scope). Falls back to nanosecond entropy if the OS RNG
// is unavailable.
func newRunID() string {
	ts := time.Now().Format("20060102-150405")
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ts + "-" + strconv.Itoa(time.Now().Nanosecond())
	}
	return ts + "-" + hex.EncodeToString(b[:])
}

// harnessOwner returns this harness process's ownership identity as
// <hostname>:<pid>. Cleanup uses it to tell its own (and other live harnesses')
// containers apart from orphans left by crashed runs.
func harnessOwner() string {
	return selfHostname() + ":" + strconv.Itoa(os.Getpid())
}

// selfHostname returns this host's name, or "unknown" when it cannot be resolved.
func selfHostname() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "unknown"
	}
	return host
}

// pidAlive reports whether the given PID string names a live process on this host.
// Signal 0 probes for existence without delivering a signal; EPERM means the
// process exists but is owned by another user — still alive.
func pidAlive(pidStr string) bool {
	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid <= 0 {
		return false
	}
	killErr := syscall.Kill(pid, 0)
	return killErr == nil || errors.Is(killErr, syscall.EPERM)
}

// isOwnerAlive reports whether the harness that owns a container (per its
// swebench.owner label) is still running on this host. An empty/unparseable
// owner (legacy pre-#598 containers) is not attributable to a live harness and
// returns false. A different host cannot be probed, so it is treated as alive —
// we never remove another host's container.
func isOwnerAlive(owner string) bool {
	host, pidStr, ok := strings.Cut(owner, ":")
	if !ok || owner == "" {
		return false
	}
	if host != selfHostname() {
		return true
	}
	return pidAlive(pidStr)
}

// containerCleanupInfo is one swebench container's cleanup-relevant facts,
// parsed from `docker ps` labels.
type containerCleanupInfo struct {
	Name      string
	Running   bool
	Owner     string
	CreatedAt time.Time
}

// selectContainersForCleanup applies the ownership-aware cleanup policy (#598)
// and returns the names to force-remove. It NEVER blanket-removes all
// swebench-labelled containers. Containers owned by a live harness (this one or
// a concurrent one) are left untouched regardless of state. Among orphans (dead
// or legacy/unlabelled owner) it removes non-running containers immediately and
// running containers only once they are older than staleTTL — a documented
// grace so a genuinely long in-flight run from a crashed-but-recent harness is
// not killed prematurely.
func selectContainersForCleanup(containers []containerCleanupInfo, now time.Time, staleTTL time.Duration, ownerAlive func(owner string) bool) []string {
	var remove []string
	for _, c := range containers {
		if ownerAlive(c.Owner) {
			continue // owned by a live harness — never touch concurrent work
		}
		if c.Running && now.Sub(c.CreatedAt) < staleTTL {
			continue // orphaned but still within the stale grace — leave it
		}
		remove = append(remove, c.Name)
	}
	return remove
}

// CleanupStale removes orphaned swebench containers from prior/crashed runs using
// the ownership-aware policy (#598). It queries every swebench-labelled container
// but removes only those NOT owned by a live harness — never blanket-removing all
// of them, so a concurrent harness's active container is preserved.
func (r *DockerRunner) CleanupStale(ctx context.Context) {
	cmd := exec.CommandContext(ctx, "docker", "ps", "-a",
		"--filter", "label="+labelSwebench,
		"--format", "{{.Names}}\t{{.State}}\t{{.Label \""+labelOwner+"\"}}\t{{.Label \""+labelCreatedAt+"\"}}")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return // best-effort
	}

	var infos []containerCleanupInfo
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 4 {
			continue
		}
		created, _ := time.Parse(time.RFC3339, fields[3]) // zero time on legacy/unparseable → treated as old
		infos = append(infos, containerCleanupInfo{
			Name:      fields[0],
			Running:   fields[1] == "running",
			Owner:     fields[2],
			CreatedAt: created,
		})
	}

	staleTTL := r.StaleTTL
	if staleTTL <= 0 {
		staleTTL = time.Hour
	}
	for _, name := range selectContainersForCleanup(infos, time.Now(), staleTTL, isOwnerAlive) {
		log.Printf("cleaning up orphaned container: %s", name)
		_ = dockerCmd(ctx, "rm", "-f", name)
	}
}

// watchdogTimeout is how long the host waits on the agent exec before force-killing
// it: the benchmark deadline plus a bounded reporting/teardown grace. It is strictly
// greater than r.Timeout so the child can emit its summary line on its own timeout.
func (r *DockerRunner) watchdogTimeout() time.Duration {
	grace := r.WatchdogGrace
	if grace <= 0 {
		grace = defaultWatchdogGrace
	}
	return r.Timeout + grace
}

// errWatchdogKill marks the case where the host watchdog force-killed the agent
// exec because the child ran past the benchmark deadline plus the reporting grace.
// It is distinct from a clean child timeout (where the child emits its own summary
// with termination_reason "timeout" and exits on its own).
var errWatchdogKill = errors.New("host watchdog killed agent exec (exceeded deadline + reporting grace)")

// terminationWatchdogKill is the termination reason recorded when the host
// watchdog fired, as opposed to the child-reported "timeout".
const terminationWatchdogKill = "watchdog_kill"

// classifyAgentRun folds the raw agent exec result into a final summary and error,
// distinguishing a host watchdog kill from a clean child exit. When the watchdog
// fired the child never finished reporting, so its termination reason is overridden
// to watchdog_kill and the error carries errWatchdogKill. Otherwise the child's own
// summary (including a "timeout" reason it emitted before exiting) is preserved and
// any exec error is wrapped verbatim.
func classifyAgentRun(summary AgentSummary, agentErr error, watchdogFired bool) (AgentSummary, error) {
	if watchdogFired {
		summary.TerminationReason = terminationWatchdogKill
		return summary, fmt.Errorf("agent-runner: %w", errWatchdogKill)
	}
	if agentErr != nil {
		return summary, fmt.Errorf("agent-runner: %w", agentErr)
	}
	return summary, nil
}
