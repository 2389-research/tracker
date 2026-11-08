// ABOUTME: Resolves the native model-turn policy for warm continuation.
// ABOUTME: Keeps unlimited and invalid negative policies intact through disk overrides.
package handlers

// resolveMaxTurns returns the warm-continue MaxTurns override for nodeID under
// workingDir when one is present, else base. A warm continue only ever RAISES
// the budget, so an override that does not exceed base (stale/smaller, or left
// by a different workflow that reused the node ID) is ignored, as is one past
// the ceiling (corruption). Overrides are scoped per working dir + node ID.
// Keeps the branch out of buildConfig (already a long flat attr sequence). #318.
func resolveMaxTurns(workingDir, nodeID string, base int) int {
	if base < 0 {
		return base // Never cap unlimited turns or mask an invalid negative policy.
	}
	override := readMaxTurnsOverride(workingDir, nodeID)
	if override <= base || override > maxTurnsOverrideCeiling {
		return base
	}
	return override
}
