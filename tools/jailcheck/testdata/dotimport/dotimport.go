// ABOUTME: jailcheck fixture — dot-importing os hides every mutating call.
// ABOUTME: Must produce exactly one violation flagging the dot-import itself.
package dotimport

import . "os"

// rawWrite calls WriteFile as a bare identifier (no os. selector) thanks to the
// dot-import — the import itself must be flagged.
func rawWrite(path string, data []byte) error {
	return WriteFile(path, data, 0o644)
}
