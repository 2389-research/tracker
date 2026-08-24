// ABOUTME: Small shared parsing helpers for the typed node-config accessors.
// ABOUTME: Split out of node_config.go to keep that file under the size gate.
package pipeline

import "strings"

// parseBoolAttr returns true if v is one of the accepted truthy spellings
// for a tracker node attribute: "true", "1", "yes", "y", "on", "TRUE", etc.
// All other values (including empty string) return false. Used by typed
// node-config accessors to read boolean attrs without per-call ParseBool
// boilerplate.
func parseBoolAttr(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "y", "on":
		return true
	}
	return false
}
