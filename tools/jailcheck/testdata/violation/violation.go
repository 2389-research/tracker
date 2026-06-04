// ABOUTME: jailcheck fixture — a tool with unguarded os.* mutations (jail bypass).
// ABOUTME: Must produce exactly three violations: os.WriteFile, os.Remove, os.MkdirAll.
package violation

import "os"

// rawWrite bypasses the ExecutionEnvironment seam — a jail bypass.
func rawWrite(path, content string) error {
	if err := os.MkdirAll("dir", 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// rawDelete bypasses the seam for a destructive op.
func rawDelete(path string) error {
	return os.Remove(path)
}

// readOnly is fine — read-only os.* is not a bypass and must NOT be flagged.
func readOnly(path string) ([]byte, error) {
	return os.ReadFile(path)
}
