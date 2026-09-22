// ABOUTME: Regression guard for issues #349/#656 — FinalCommit in build_product.dip
// ABOUTME: must be a DETERMINISTIC tool node so it cannot author unreviewed product source.
package pipeline

import "testing"

const finalCommitNodeID = "FinalCommit"

// TestBuildProductFinalCommitIsDeterministicTool pins the #656 fix: FinalCommit
// is a tool node running FinalCommit.sh, NOT an auto_status agent.
//
// History: #349 made FinalCommit a jailed agent (`writable_paths: .git/**,
// .ai/**`, `commit_only`) because an LLM commit node once authored an entire
// unreviewed milestone; the jail was the mitigation. #656 replaced the agent
// with a fixed script — which cannot author product source at all, so the
// jail's risk class is eliminated by construction, not merely bounded — and
// removed the failure mode where the agent's mandatory early STATUS:fail made a
// clean-tree (already-committed) build fail and dead-stop the run. A tool node
// is deterministic: a clean tree is always a success.
//
// Guard: if a future edit reintroduces an agent/LLM here, this fails — the
// #349 case-study risk and the #656 clean-tree crash both return with it.
func TestBuildProductFinalCommitIsDeterministicTool(t *testing.T) {
	g := loadBuildProduct(t)

	n, ok := g.Nodes[finalCommitNodeID]
	if !ok {
		t.Fatalf("build_product.dip has no %s node", finalCommitNodeID)
	}

	if n.Handler != "tool" {
		t.Fatalf("%s handler = %q, want \"tool\" — FinalCommit must be a deterministic tool node, not an LLM/codergen node, so a clean tree is a guaranteed success and it cannot author unreviewed product source (issues #349, #656)", finalCommitNodeID, n.Handler)
	}

	// A tool node carries neither the fs-jail nor commit_only — both were
	// agent-only mitigations, meaningless (and confusing) on a fixed script.
	cfg := n.AgentConfig(nil)
	if cfg.WritablePathsSet {
		t.Errorf("%s is a tool node but still declares writable_paths — the fs-jail was an agent-node mitigation and is moot on a fixed script (issue #656)", finalCommitNodeID)
	}
	if _, hasCommitOnly := n.Attrs["commit_only"]; hasCommitOnly {
		t.Errorf("%s is a tool node but still declares commit_only — that was an agent-node backstop and is meaningless here (issue #656)", finalCommitNodeID)
	}

	// It must actually run the deterministic committer. The adapter lowers a
	// tool node's command_file/command into the tool_command attr.
	if n.Attrs["tool_command"] == "" {
		t.Errorf("%s has no tool_command — a tool node must run FinalCommit.sh (issue #656)", finalCommitNodeID)
	}
}
