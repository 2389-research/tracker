// ABOUTME: Tests mid-run steering — the steer command's registry lookup, the
// ABOUTME: non-blocking send, and lifecycle (no active run / after completion).
package chatops

import (
	"strings"
	"testing"
)

// registerSteeringTest creates + registers a steering channel and returns it,
// simulating what launch does after a successful Start.
func (r *Runner) registerSteeringTest(threadTS string) chan map[string]string {
	ch := make(chan map[string]string, steerBufSize)
	r.registerSteering(threadTS, ch)
	return ch
}

func TestSteerRun_NoActiveRun(t *testing.T) {
	r, _, uis := newTestRunner(t, t.TempDir())
	ui := uis.newUI("C1", "T1")
	r.steerRun(ui, "T1", "focus on tests")
	posts := strings.Join(fakePosts(uis.ui("T1")), "\n")
	if !strings.Contains(posts, "No active run") {
		t.Errorf("steer with no run should say so, got:\n%s", posts)
	}
}

func TestSteerRun_EmptyGuidance(t *testing.T) {
	r, _, uis := newTestRunner(t, t.TempDir())
	ui := uis.newUI("C1", "T1")
	r.steerRun(ui, "T1", "   ")
	posts := strings.Join(fakePosts(uis.ui("T1")), "\n")
	if !strings.Contains(posts, "Usage") {
		t.Errorf("empty steer should show usage, got:\n%s", posts)
	}
}

func TestSteerRun_DeliversToChannel(t *testing.T) {
	r, _, uis := newTestRunner(t, t.TempDir())
	ui := uis.newUI("C1", "T1")
	ch := r.registerSteeringTest("T1") // simulate an active run's registered channel

	r.steerRun(ui, "T1", "prefer the smaller change")

	select {
	case update := <-ch:
		if update[steerGuidanceKey] != "prefer the smaller change" {
			t.Errorf("steer payload = %v, want guidance note under %q", update, steerGuidanceKey)
		}
	default:
		t.Fatal("steer did not deliver to the steering channel")
	}
	posts := strings.Join(fakePosts(uis.ui("T1")), "\n")
	if !strings.Contains(posts, "steering queued") {
		t.Errorf("steer should confirm, got:\n%s", posts)
	}
}

func TestSteerRun_AfterUnregisterIsInactive(t *testing.T) {
	r, _, uis := newTestRunner(t, t.TempDir())
	ui := uis.newUI("C1", "T1")
	r.registerSteeringTest("T1")
	r.unregisterSteering("T1") // run finished

	r.steerRun(ui, "T1", "too late")
	posts := strings.Join(fakePosts(uis.ui("T1")), "\n")
	if !strings.Contains(posts, "No active run") {
		t.Errorf("steer after completion should report no active run, got:\n%s", posts)
	}
}
