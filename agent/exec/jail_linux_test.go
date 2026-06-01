//go:build linux

package exec

import (
	"errors"
	"testing"
)

func TestProbeLandlock_OnSupportedKernel(t *testing.T) {
	// GHA ubuntu-latest, Ubuntu 24.04, RHEL 9.4+ all have ABI v3.
	// If this test runs on an older kernel, t.Skip so the suite stays green.
	err := ProbeLandlock()
	if errors.Is(err, ErrLandlockUnavailable) {
		t.Skipf("kernel doesn't support Landlock ABI v3: %v", err)
	}
	if err != nil {
		t.Errorf("ProbeLandlock = %v, want nil on a supported kernel", err)
	}
}
