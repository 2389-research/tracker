// ABOUTME: jailcheck fixture — capturing a mutating os.* function value bypasses
// ABOUTME: a call-only lint. Must produce exactly one violation (os.WriteFile).
package funcvalue

import "os"

// captureWrite hoists os.WriteFile into a variable then calls it — a call-only
// analyzer would miss this; the selector reference must still be flagged.
func captureWrite(path string, data []byte) error {
	wf := os.WriteFile
	return wf(path, data, 0o644)
}

// captureRead does the same with a read-only func — must NOT be flagged.
func captureRead() func(string) ([]byte, error) {
	return os.ReadFile
}
