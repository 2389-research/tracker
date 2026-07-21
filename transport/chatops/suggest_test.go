// ABOUTME: Tests that an unresolvable request suggests workflows instead of dead-ending.
package chatops

import (
	"context"
	"strings"
	"testing"
)

func TestRunner_UnknownWorkflowSuggests(t *testing.T) {
	r, _, uis := newTestRunner(t, t.TempDir())
	r.OnMention(context.Background(), "C", "T1", "run definitely_not_a_workflow_xyz")
	posts := strings.Join(fakePosts(uis.ui("T1")), "\n")
	if !strings.Contains(posts, "Unknown workflow") {
		t.Errorf("expected an unknown-workflow message:\n%s", posts)
	}
	if !strings.Contains(posts, "Try one of:") {
		t.Errorf("expected a workflow suggestion:\n%s", posts)
	}
}
