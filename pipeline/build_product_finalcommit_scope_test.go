// ABOUTME: Regression guard for issue #349 — FinalCommit in build_product.dip must
// ABOUTME: carry the MECHANICAL writable_paths fs-jail (git + .ai/ only) under prefer mode (#648).
package pipeline

import "testing"

const finalCommitNodeID = "FinalCommit"

// TestBuildProductFinalCommitScopeGuard pins the scope defense on FinalCommit
// (a late-pipeline agent node with commit ability and a fat context window; the
// #349 case-study node that authored an unreviewed milestone).
//
// #349 added a MECHANICAL `writable_paths: .git/**, .ai/**` fs-jail (#272) as
// the primary defense. #642 removed it because a refused jail became a hard,
// non-retryable fail and build_product must run on macOS / Linux < 6.2, where
// Landlock is unavailable. #648 re-declares it under `writable_paths_mode:
// prefer`: enforced exactly as `require` wherever Landlock is available;
// elsewhere the node runs UNJAILED with a recorded jail_degraded event instead
// of refusing. This test pins all three halves — the declaration, the exact
// git+.ai/ scope, and prefer mode — plus commit_only as the backstop that is
// the sole defense on a host without Landlock.
func TestBuildProductFinalCommitScopeGuard(t *testing.T) {
	g := loadBuildProduct(t)

	n, ok := g.Nodes[finalCommitNodeID]
	if !ok {
		t.Fatalf("build_product.dip has no %s node", finalCommitNodeID)
	}

	cfg := n.AgentConfig(nil)
	if !cfg.WritablePathsSet {
		t.Fatalf("%s must declare writable_paths so the fs-jail bounds its tools on hosts that can enforce it (issues #349, #648)", finalCommitNodeID)
	}

	// The jail must allow git internals (so commits work) and .ai/ scratch, and
	// nothing else (so product source is unwritable). Order-independent set check.
	want := map[string]bool{".git/**": true, ".ai/**": true}
	got := map[string]bool{}
	for _, p := range cfg.WritablePaths {
		got[p] = true
	}
	for w := range want {
		if !got[w] {
			t.Errorf("%s writable_paths missing %q (have %v) — git+.ai/ scope required for a commit-only node (issue #349)", finalCommitNodeID, w, cfg.WritablePaths)
		}
	}
	for g := range got {
		if !want[g] {
			t.Errorf("%s writable_paths has unexpected entry %q (have %v) — widening the jail beyond git+.ai/ re-opens the product-source write path (issue #349)", finalCommitNodeID, g, cfg.WritablePaths)
		}
	}

	// prefer, not require: build_product must still RUN on macOS / Linux < 6.2
	// (#642). Flipping this back to require re-breaks those hosts.
	if cfg.WritablePathsMode != WritablePathsModePrefer {
		t.Errorf("%s writable_paths_mode = %q, want %q — under require the node refuses to start on any host without Landlock and build_product must run there (issues #642, #648)", finalCommitNodeID, cfg.WritablePathsMode, WritablePathsModePrefer)
	}

	// The mechanical jail must not have silently displaced the prompt/system-prompt
	// backstop: commit_only stays on — on a host without Landlock it is the
	// ONLY defense for the Bash subprocess.
	if !cfg.CommitOnly {
		t.Errorf("%s lost commit_only — the prompt/system-prompt backstop must remain alongside the (prefer-mode) fs-jail (issues #349, #648)", finalCommitNodeID)
	}
}
