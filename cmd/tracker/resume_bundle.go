// ABOUTME: Strict bundle identity verification on resume — rejects mismatches
// ABOUTME: between checkpoint identity and current bundle unless --force-bundle-mismatch.
package main

import (
	"errors"
	"fmt"
)

var errBundleIdentityMismatch = errors.New("bundle identity mismatch on resume")

// verifyResumeBundle checks the checkpoint's stored bundle identity against
// the current bundle's identity. Returns nil if they match (or if force is
// true). Any difference — including empty-on-one-side — is a mismatch.
//
// The empty-vs-empty case (resume a .dip-started run against a .dip) is the
// only no-identity-change case that's silently allowed; it preserves existing
// behavior for plain .dip workflows.
func verifyResumeBundle(checkpointIdentity, currentIdentity string, force bool) error {
	if checkpointIdentity == currentIdentity {
		return nil
	}
	if force {
		return nil
	}
	return fmt.Errorf("%w\n  run was started against: %s\n  current bundle:          %s\nThe pipeline source has changed since this run was started. To resume against a different bundle, pass --force-bundle-mismatch.",
		errBundleIdentityMismatch,
		displayIdentity(checkpointIdentity),
		displayIdentity(currentIdentity),
	)
}

// displayIdentity formats a bundle identity for human-readable error output.
// An empty identity means the source was a plain .dip file (no content-
// addressed identity); a non-empty value is shown as-is ("sha256:<hex>").
func displayIdentity(id string) string {
	if id == "" {
		return "(none — plain .dip)"
	}
	return id
}
