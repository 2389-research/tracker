// ABOUTME: Tests that chat event rendering runs behind a bounded buffered handler.
// ABOUTME: A slow transport sink must not stall the engine goroutine, and the flush must still land.
package chatops

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tracker "github.com/2389-research/tracker"
)

// blockingUI models a chat transport whose network calls hang. Event-driven
// posts (the notifier's "✅ <node> done") block until released; the runner's own
// acknowledgement is let through so OnMention itself does not deadlock.
type blockingUI struct {
	release chan struct{}

	mu    sync.Mutex
	posts []string
}

func newBlockingUI() *blockingUI { return &blockingUI{release: make(chan struct{})} }

func (b *blockingUI) PostGate(Gate) error { return nil }

func (b *blockingUI) Post(text string) error {
	b.mu.Lock()
	b.posts = append(b.posts, text)
	b.mu.Unlock()
	if strings.HasPrefix(text, "✅") {
		<-b.release
	}
	return nil
}

// TestRunner_SlowChatSinkDoesNotStallTheRun is the point of SIFT-SUB-11-01:
// notifier/status rendering used to be installed directly as Config.EventHandler,
// and handlers run synchronously on the engine goroutine, so a hung Slack call
// halted unrelated pipeline work and held a RunManager slot open.
func TestRunner_SlowChatSinkDoesNotStallTheRun(t *testing.T) {
	workDir := t.TempDir()
	writeWorkflow(t, workDir, "quick.dip", quickDip)

	ui := newBlockingUI()
	t.Cleanup(func() { close(ui.release) })

	rm := tracker.NewRunManager()
	r := NewRunner(rm, RunnerDeps{
		NewThreadUI: func(_, _ string) ThreadUI { return ui },
		WorkDir:     workDir,
		RunsBase:    t.TempDir(),
		NewID:       seqIDs(),
		ConfigBase:  tracker.Config{Format: "dip", LLMClient: stubCompleter{}},
	})

	r.OnMention(context.Background(), "C1", "T1", "quick")
	run, ok := rm.Get("T1")
	if !ok {
		t.Fatal("run T1 not tracked")
	}

	select {
	case <-run.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("run did not finish while the chat sink was blocked — the engine goroutine is stalled on transport I/O")
	}
}

// TestRunner_BufferedEventsAreFlushed guards the other half: buffering must not
// swallow the progress posts. They arrive asynchronously, but the handler is
// closed (and flushed) when the run finishes, before the outcome is delivered.
func TestRunner_BufferedEventsAreFlushed(t *testing.T) {
	workDir := t.TempDir()
	writeWorkflow(t, workDir, "quick.dip", quickDip)
	r, rm, uis := newTestRunner(t, workDir)

	r.OnMention(context.Background(), "C", "T1", "quick")
	run, ok := rm.Get("T1")
	if !ok {
		t.Fatal("run not tracked")
	}
	waitDone(t, run)

	ui := uis.ui("T1")
	waitForPost(t, ui, "done", 5*time.Second)
}
