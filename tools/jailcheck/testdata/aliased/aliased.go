// ABOUTME: jailcheck fixture — aliased os import still bypasses the jail.
// ABOUTME: Must produce exactly one violation (stdos.WriteFile) via import resolution.
package aliased

import stdos "os"

// rawWriteAliased bypasses the seam through an aliased os import.
func rawWriteAliased(path, content string) error {
	return stdos.WriteFile(path, []byte(content), 0o644)
}

// readOnlyAliased uses a read-only aliased os.* call — must NOT be flagged.
func readOnlyAliased(path string) ([]byte, error) {
	return stdos.ReadFile(path)
}
