// ABOUTME: Shared formatting helpers for content-addressed .dipx bundle identities.
// ABOUTME: Lives in internal/ so it's not part of the public API surface.

// Package bundleid provides shared formatting helpers for content-addressed
// .dipx bundle identities. Lives in internal/ so it's not part of the public
// API surface — callers outside this module should not depend on it.
package bundleid

// DisplayForLog formats a bundle identity for inclusion in human-readable
// log messages and error text. An empty identity (plain .dip runs that
// never had a content-addressed bundle) renders as a parenthesized
// explanation so users can tell the difference between "no bundle" and
// "missing field". Non-empty identities are returned as-is (typically
// "sha256:<64 hex>").
func DisplayForLog(id string) string {
	if id == "" {
		return "(none — plain .dip)"
	}
	return id
}
