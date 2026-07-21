// ABOUTME: Verifies Config.SteeringChan is forwarded into the engine options,
// ABOUTME: closing the transport-boundary "Control a run" steering seam.
package tracker

import "testing"

// TestBuildEngineOpts_ForwardsSteeringChan asserts that setting Config.SteeringChan
// contributes exactly one additional engine option (WithSteeringChan). The
// engine-level behavior of that option is covered by pipeline/engine_steering_test.go;
// this guards the forward so a refactor can't silently drop the wiring.
func TestBuildEngineOpts_ForwardsSteeringChan(t *testing.T) {
	base := Config{}
	withoutN := len(buildEngineOpts(base, nil))

	ch := make(chan map[string]string)
	base.SteeringChan = ch
	withN := len(buildEngineOpts(base, nil))

	if withN != withoutN+1 {
		t.Errorf("SteeringChan should add exactly one engine option: got %d, want %d",
			withN, withoutN+1)
	}
}

// TestBuildEngineOpts_NilSteeringChanIsNoop confirms the default (nil) adds no
// option, so existing callers are unaffected.
func TestBuildEngineOpts_NilSteeringChanIsNoop(t *testing.T) {
	a := len(buildEngineOpts(Config{}, nil))
	b := len(buildEngineOpts(Config{SteeringChan: nil}, nil))
	if a != b {
		t.Errorf("nil SteeringChan changed option count: %d vs %d", a, b)
	}
}
