// ABOUTME: writable_paths_mode (#648) — the two jail enforcement modes and the
// ABOUTME: exact-match validator shared by the dippin adapter and validateGraph.
package pipeline

import "fmt"

// AttrWritablePathsMode is the agent-node attribute selecting the jail's
// enforcement mode (#648). Delivered from a .dip file via the agent `params:`
// passthrough until dippin grows a typed field.
const AttrWritablePathsMode = "writable_paths_mode"

const (
	// WritablePathsModeRequire is the default: every refuse-to-start gate
	// (bad globs / working_dir, non-native backend, Landlock unavailable)
	// refuses the node. Byte-identical to the pre-#648 behaviour (#272, #642).
	WritablePathsModeRequire = "require"
	// WritablePathsModePrefer degrades ONLY a host-capability refusal (Landlock
	// ABI < 3, non-Linux) to an UNJAILED run with a recorded jail_degraded
	// event. Authoring errors (malformed globs, bad working_dir) and backend
	// refusals (claude-code / acp / unknown) still refuse. A prefer node is
	// never sandboxed on a host without Landlock — see the #648 spec §3.
	WritablePathsModePrefer = "prefer"
)

// ValidateWritablePathsMode reports whether raw is exactly one of the two
// legal modes. Fail-closed on purpose: no trimming, no case-folding — "Prefer",
// "prefer " and "" (present-but-empty) are all rejected, so a typo can never
// silently select a mode the author did not write. Callers wrap the error with
// the node ID.
func ValidateWritablePathsMode(raw string) error {
	switch raw {
	case WritablePathsModeRequire, WritablePathsModePrefer:
		return nil
	}
	return fmt.Errorf("invalid %s %q: must be exactly %q or %q", AttrWritablePathsMode, raw, WritablePathsModeRequire, WritablePathsModePrefer)
}

// validateWritablePathsMode is the tracker-owned graph check for #648: any
// node carrying the attr with a value other than the two constants fails
// validation, naming the node. Runs on every graph source (dip, DOT,
// programmatic) — it is not covered by dippin's own validation, so it is not
// gated on DippinValidated.
func validateWritablePathsMode(g *Graph, ve *ValidationError) {
	for _, node := range g.Nodes {
		raw, ok := node.Attrs[AttrWritablePathsMode]
		if !ok {
			continue
		}
		if err := ValidateWritablePathsMode(raw); err != nil {
			ve.add(fmt.Sprintf("node %q has %v", node.ID, err))
		}
	}
}

// writablePathsMode resolves the node's mode for AgentConfig (#648):
// absent/empty normalizes to require. A present-but-invalid value is a load
// error (adapter + validateGraph), so by the time a handler reads this it is
// one of the two constants; the raw value is carried through untouched (no
// trimming / case-folding — see ValidateWritablePathsMode).
func (n *Node) writablePathsMode() string {
	if raw := n.Attrs[AttrWritablePathsMode]; raw != "" {
		return raw
	}
	return WritablePathsModeRequire
}
