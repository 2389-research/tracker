// ABOUTME: Tests that Engine.Close cancels a cancellable interviewer it owns (#478).
package tracker

import (
	"context"
	"sync"
	"testing"
)

type cancelSpyInterviewer struct {
	mu       sync.Mutex
	canceled bool
}

func (c *cancelSpyInterviewer) Ask(string, []string, string) (string, error) { return "", nil }
func (c *cancelSpyInterviewer) Cancel() {
	c.mu.Lock()
	c.canceled = true
	c.mu.Unlock()
}
func (c *cancelSpyInterviewer) wasCanceled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.canceled
}

// TestEngine_CloseCancelsInterviewer proves the engine cancels a cancellable
// interviewer (e.g. the webhook callback server) on Close, so a library caller
// that lets the library own the interviewer doesn't leak it past the run.
func TestEngine_CloseCancelsInterviewer(t *testing.T) {
	graph, err := parsePipelineSource(quickDip, "dip")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	iv := &cancelSpyInterviewer{}

	eng, err := NewEngineFromGraph(context.Background(), graph, Config{
		WorkingDir:  t.TempDir(),
		LLMClient:   successStub(),
		Interviewer: iv,
	})
	if err != nil {
		t.Fatalf("NewEngineFromGraph: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !iv.wasCanceled() {
		t.Fatal("expected Close to cancel the interviewer")
	}
}
