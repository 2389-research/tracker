// ABOUTME: Runs the transport conformance suite against ThreadInterviewer.
package chatops

import (
	"testing"

	"github.com/2389-research/tracker/transport/conformance"
)

// TestThreadInterviewer_Conformance proves the Slack transport's interviewer
// honours the whole gate contract by running the shared suite. A future
// web/mobile transport runs the same suite the same way — the executable
// definition of a correct interviewer.
func TestThreadInterviewer_Conformance(t *testing.T) {
	conformance.RunInterviewerSuite(t, func() conformance.Subject {
		ui := newFakeUI()
		iv := NewThreadInterviewer(ui, seqIDs())
		return conformance.Subject{
			Interviewer: iv,
			Answer: func(t *testing.T, reply string) string {
				g := awaitGate(t, ui)
				ans := GateAnswer{}
				if g.Kind == GateFreeform {
					ans.Freeform = reply
				} else {
					ans.Choice = reply
				}
				if !iv.Resolve(g.ID, ans) {
					t.Fatalf("Resolve returned false for pending gate %s", g.ID)
				}
				return g.Prompt
			},
			Cancel: iv.Cancel,
		}
	})
}
