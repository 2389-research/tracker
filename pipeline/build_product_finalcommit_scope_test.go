// ABOUTME: Regression guard for FinalCommit's scope defense in build_product.dip:
// ABOUTME: commit_only must stay on, and writable_paths must stay off until a
// ABOUTME: cross-platform (degrade-with-warning) jail mode exists (#349 vs #642).
package pipeline

import "testing"

const finalCommitNodeID = "FinalCommit"

// TestBuildProductFinalCommitScopeGuard pins the current scope defense on
// FinalCommit (a late-pipeline agent node with commit ability and a fat context
// window; the #349 case-study node that authored an unreviewed milestone).
//
// #349 added a MECHANICAL `writable_paths: .git/**, .ai/**` fs-jail as the
// primary defense. #642 removed it: the jail refuses to start anywhere Landlock
// ABI v3 is unavailable (macOS, Linux < 6.2, non-native backends), and
// build_product must run on macOS — with the jail declared the node could
// never finish there. dippin has no platform-gated attribute, so until a
// degrade-with-warning `writable_paths` mode exists (#642 follow-up) the
// commit_only prompt + system-prompt guard is the sole scope defense and this
// test pins both halves of that decision. Re-adding writable_paths here must
// come with the cross-platform mode, not on its own.
func TestBuildProductFinalCommitScopeGuard(t *testing.T) {
	g := loadBuildProduct(t)

	n, ok := g.Nodes[finalCommitNodeID]
	if !ok {
		t.Fatalf("%s node missing from build_product.dip (issue #349)", finalCommitNodeID)
	}

	cfg := n.AgentConfig(nil)
	if cfg.WritablePathsSet {
		t.Errorf("%s declares writable_paths %v — the jail refuses to start on macOS / Linux < 6.2 and build_product must run there (issue #642); re-add only alongside a degrade-with-warning jail mode", finalCommitNodeID, cfg.WritablePaths)
	}

	// commit_only is the remaining scope defense: it must stay on.
	if !cfg.CommitOnly {
		t.Errorf("%s lost commit_only — it is the sole scope backstop while writable_paths is off (issues #349, #642)", finalCommitNodeID)
	}
}
