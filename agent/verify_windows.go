//go:build windows

// ABOUTME: Verify-after-edit stubs for Windows. The POSIX runner (process-group
// ABOUTME: setpgid/kill, sh -c) has no Windows equivalent, so verification is a
// ABOUTME: safe no-op here: the resolvers return nil and callers skip the loop.
package agent

import (
	"context"
	"errors"
)

// verifier mirrors the POSIX type's fields so shared/test code compiles, but it
// is never constructed at runtime on Windows — both resolvers return nil.
type verifier struct {
	cmd      string
	broadCmd string
	workDir  string
}

// newVerifier is a no-op on Windows: the verify-after-edit loop has no portable
// runner here, so verification is skipped (forward progress) rather than wrongly
// reporting pass/fail. Callers already treat a nil verifier as "disabled".
func newVerifier(cfg SessionConfig) *verifier {
	_ = cfg
	return nil
}

// resolveBreachVerifier is a no-op on Windows for the same reason as newVerifier;
// a nil result means the breach verify pass does not run (BreachVerifyNotRun).
func resolveBreachVerifier(cfg SessionConfig) *verifier {
	_ = cfg
	return nil
}

// run is unreachable on Windows (both resolvers return nil) but must exist for
// the shared callers in session.go to compile. It fails closed: a real
// execution error is never swallowed into a false "passed".
func (v *verifier) run(ctx context.Context) (verifyResult, error) {
	_ = ctx
	return verifyResult{Command: v.cmd}, errors.New("verify-after-edit is not supported on windows")
}
