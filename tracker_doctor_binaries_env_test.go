// ABOUTME: Tests the environment tracker doctor's `claude --version` probe runs with.
// ABOUTME: It must match the claude-code launch (buildEnv): no provider keys unless TRACKER_PASS_API_KEYS=1.
package tracker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The doctor probe runs whatever `claude` PATH names, the same binary the
// claude-code backend launches without provider keys, so it must not hand
// that binary the keys either (#660).
func TestDoctorClaudeProbeGetsLaunchEnv(t *testing.T) {
	binDir := t.TempDir()
	dump := filepath.Join(t.TempDir(), "env.txt")
	script := "#!/bin/sh\nenv > '" + dump + "'\necho 9.9.9\n"
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TRACKER_PASS_API_KEYS", "")
	t.Setenv("ANTHROPIC_API_KEY", "sentinel-provider-key")

	res := checkOtherBinaries(context.Background(), "claude-code")
	got, err := os.ReadFile(dump)
	if err != nil {
		t.Fatalf("the doctor probe never ran claude (result %+v): %v", res, err)
	}
	if strings.Contains(string(got), "sentinel-provider-key") {
		t.Error("tracker doctor's claude --version probe saw ANTHROPIC_API_KEY, which the claude-code launch strips")
	}
}
