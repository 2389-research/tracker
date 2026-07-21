// ABOUTME: Tests the ActiveRuns dashboard accessor.
package chatops

import "testing"

func TestActiveRuns_EmptyWhenNoRuns(t *testing.T) {
	r, _, _ := newTestRunner(t, t.TempDir())
	if got := r.ActiveRuns(); len(got) != 0 {
		t.Errorf("expected no active runs, got %d", len(got))
	}
}
