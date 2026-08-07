// ABOUTME: Secure staging of caller-supplied file inputs into the run workdir so
// ABOUTME: a jailed node reads a fixed, safe path instead of the untrusted value.
package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MaxInputFileBytes caps a staged file input (10 MiB) — generous for a spec,
// bounded against a caller streaming an unbounded file into the run dir.
const MaxInputFileBytes = 10 << 20

// inputStageDir is the fixed, deterministic location under the workdir where
// file inputs are staged. It is derived only from the declared input NAME (not
// the caller's path/contents), so a workflow's shell can read the staged file at
// a known-safe path — the untrusted ${inputs.*} value never enters the command.
const inputStageDir = ".tracker/inputs"

// StageInputFile writes data to <workDir>/.tracker/inputs/<name> (mode 0600,
// symlink-refusing) and returns the workdir-relative staged path. name must be a
// single safe path segment; data must be within MaxInputFileBytes.
func StageInputFile(workDir, name string, data []byte) (string, error) {
	if err := validateInputName(name); err != nil {
		return "", err
	}
	if len(data) > MaxInputFileBytes {
		return "", fmt.Errorf("input %q is %d bytes, over the %d-byte cap", name, len(data), MaxInputFileBytes)
	}
	dir := filepath.Join(workDir, filepath.FromSlash(inputStageDir))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := refuseIfSymlink(dir); err != nil {
		return "", err
	}
	if err := writeCaptureFile(filepath.Join(dir, name), data, 0o600); err != nil {
		return "", err
	}
	return filepath.ToSlash(filepath.Join(inputStageDir, name)), nil
}

// validateInputName rejects anything that is not a single safe path segment, so
// a staged file can never escape the inputs directory.
func validateInputName(name string) error {
	if name == "" || name == "." || name == ".." ||
		strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return fmt.Errorf("invalid input name %q (must be a single path segment)", name)
	}
	return nil
}
