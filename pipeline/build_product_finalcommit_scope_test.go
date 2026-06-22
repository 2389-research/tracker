// ABOUTME: Regression guard for issue #349 — FinalCommit in build_product.dip must
// ABOUTME: carry a MECHANICAL writable_paths fs-jail bounding writes to git + .ai/ only.
package pipeline

import "testing"

const finalCommitNodeID = "FinalCommit"

// TestBuildProductFinalCommitWritablePathsJail pins the primary, mechanical
// scope guard for #349: FinalCommit (a late-pipeline agent node with commit
// ability and a fat context window) must declare writable_paths so the native
// fs-jail (#272) bounds its file-mutation tools — Bash + descendants AND
// in-process Write/Edit/ApplyPatch — to git internals + tracker's .ai/ scratch.
// This is what physically prevents the case-study failure (FinalCommit authored
// an entire unreviewed milestone): with the jail, `git add`/`git commit` still
// work but product source cannot be written even if the prompt is subverted.
//
// The prompt + commit_only system-prompt guard remain the backstop and are
// pinned elsewhere (handlers TestCommitOnlyScopeGuard*); this test pins the
// mechanical layer that does not rely on the model obeying instructions.
func TestBuildProductFinalCommitWritablePathsJail(t *testing.T) {
	g := loadBuildProduct(t)

	n, ok := g.Nodes[finalCommitNodeID]
	if !ok {
		t.Fatalf("%s node missing from build_product.dip (issue #349)", finalCommitNodeID)
	}

	cfg := n.AgentConfig(nil)
	if !cfg.WritablePathsSet {
		t.Fatalf("%s must declare writable_paths so the fs-jail bounds its tools — without it the node can author unreviewed product code (issue #349)", finalCommitNodeID)
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

	// The mechanical jail must not have silently displaced the prompt/system-prompt
	// backstop: commit_only stays on as defense-in-depth.
	if !cfg.CommitOnly {
		t.Errorf("%s lost commit_only — the prompt/system-prompt backstop must remain alongside the fs-jail (issue #349)", finalCommitNodeID)
	}
}
