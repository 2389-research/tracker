//go:build windows

// ABOUTME: Windows no-op stub — the POSIX /dev/null device node does not apply (#423).
package main

// probeDevNull is a no-op on Windows: there is no POSIX /dev/null device node,
// so the probe is skipped and the build stays green.
func probeDevNull() error { return nil }
