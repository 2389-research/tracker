// ABOUTME: jailcheck fixture — non-os jail bypasses across watched packages.
// ABOUTME: Must flag exec.Command, exec.CommandContext, ioutil.WriteFile, syscall.Unlink (4).
package subprocess

import (
	"context"
	"io/ioutil"
	"os/exec"
	"syscall"
)

// spawnDirect bypasses env.ExecCommand (and so the jail's CommandWrapper).
func spawnDirect() error {
	return exec.Command("true").Run()
}

// spawnCtx is the context variant — also a subprocess bypass.
func spawnCtx(ctx context.Context) error {
	return exec.CommandContext(ctx, "sh", "-c", "echo hi").Run()
}

// deprecatedWrite bypasses env.WriteFile via the deprecated ioutil alias.
func deprecatedWrite(path string, data []byte) error {
	return ioutil.WriteFile(path, data, 0o644)
}

// lowLevelDelete bypasses env.RemoveFile via a raw syscall.
func lowLevelDelete(path string) error {
	return syscall.Unlink(path)
}

// readOnlyNotFlagged uses read-only entry points — must NOT be flagged.
func readOnlyNotFlagged(path string) ([]byte, error) {
	if _, err := exec.LookPath("sh"); err != nil {
		return nil, err
	}
	return ioutil.ReadFile(path)
}
