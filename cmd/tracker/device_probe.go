// ABOUTME: Pre-run sandbox device-node hygiene check (#423).
// ABOUTME: Verifies /dev/null is a usable char device before any git/subprocess handler runs.
package main

import (
	"errors"
	"fmt"
)

// ErrDeviceNodeUnusable — a standard sandbox device node is missing or broken.
// A suspended/restored sandbox can corrupt device nodes (e.g. /dev/null becomes
// unreadable or a regular file), silently breaking git and subprocess handlers.
var ErrDeviceNodeUnusable = errors.New("standard device node unusable")

// defaultDeviceProbe is the injectable seam. Production uses probeDevNull;
// tests override this package var (or pass a stub to checkDeviceNodes) so the
// host device is never touched.
var defaultDeviceProbe = probeDevNull

// checkDeviceNodes runs the device probe and, on failure, wraps it with a
// specific, actionable diagnostic. A nil probe resolves to defaultDeviceProbe.
func checkDeviceNodes(probe func() error) error {
	if probe == nil {
		probe = defaultDeviceProbe
	}
	if err := probe(); err != nil {
		return fmt.Errorf("%w: %v\n"+
			"a suspended/restored sandbox can corrupt device nodes; git and subprocess "+
			"handlers will fail. Recreate /dev/null (on Linux: `mknod -m 666 /dev/null "+
			"c 1 3`; device numbers differ on other platforms) or restart the sandbox, "+
			"then re-run", ErrDeviceNodeUnusable, err)
	}
	return nil
}
