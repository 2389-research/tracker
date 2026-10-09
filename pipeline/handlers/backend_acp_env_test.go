// ABOUTME: Tests the environments Tracker hands ACP children: model-run terminal commands and the --version probe.
// ABOUTME: Terminal commands get the credential-filtered exec.CommandEnv; the probe gets the agent's own environment.
package handlers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/tracker/agent"
	acp "github.com/coder/acp-go-sdk"
)

// runACPTerminal runs one command through the client handler's terminal
// methods, the path an agent's terminal/create request takes, and returns
// the command's output.
func runACPTerminal(t *testing.T, req acp.CreateTerminalRequest) string {
	t.Helper()
	h := &acpClientHandler{
		emit:       func(agent.Event) {},
		workingDir: t.TempDir(),
		toolNames:  make(map[string]string),
	}
	req.SessionId = "s1"
	resp, err := h.CreateTerminal(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateTerminal: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := h.WaitForTerminalExit(ctx, acp.WaitForTerminalExitRequest{TerminalId: resp.TerminalId, SessionId: "s1"}); err != nil {
		t.Fatalf("WaitForTerminalExit: %v", err)
	}
	out, err := h.TerminalOutput(context.Background(), acp.TerminalOutputRequest{TerminalId: resp.TerminalId, SessionId: "s1"})
	if err != nil {
		t.Fatalf("TerminalOutput: %v", err)
	}
	return out.Output
}

// The model chooses what an ACP terminal command runs, so it must not see
// Tracker's credentials, just like the native bash tool. Variables the agent
// sets on the request itself still reach the command.
func TestACPTerminalStripsSensitiveEnv(t *testing.T) {
	t.Setenv("TRACKER_PASS_ENV", "")
	t.Setenv("TRACKER_STRIP_ACP_KEYS", "")
	t.Setenv("OPENAI_COMPAT_API_KEY", "sentinel-provider-key")
	t.Setenv("TRACKER_TEST_PLAIN_VAR", "plain-value")

	out := runACPTerminal(t, acp.CreateTerminalRequest{
		Command: "sh",
		Args:    []string{"-c", `printf '%s|%s|%s' "${OPENAI_COMPAT_API_KEY-unset}" "${TRACKER_TEST_PLAIN_VAR-unset}" "${TRACKER_TEST_AGENT_VAR-unset}"`},
		Env:     []acp.EnvVariable{{Name: "TRACKER_TEST_AGENT_VAR", Value: "from-agent"}},
	})
	if strings.Contains(out, "sentinel-provider-key") {
		t.Fatalf("terminal command saw the provider key: %q", out)
	}
	if out != "unset|plain-value|from-agent" {
		t.Errorf("output = %q, want the key unset, the plain variable kept and the agent's variable added", out)
	}
}

// TRACKER_PASS_ENV=1 is the escape hatch for every model-run command, and
// ACP terminal commands honor it the same way.
func TestACPTerminalPassEnvKeepsSensitiveEnv(t *testing.T) {
	t.Setenv("TRACKER_PASS_ENV", "1")
	t.Setenv("OPENAI_COMPAT_API_KEY", "sentinel-provider-key")

	out := runACPTerminal(t, acp.CreateTerminalRequest{
		Command: "sh",
		Args:    []string{"-c", `printf '%s' "${OPENAI_COMPAT_API_KEY-unset}"`},
	})
	if out != "sentinel-provider-key" {
		t.Errorf("output = %q, want TRACKER_PASS_ENV=1 to pass the key through", out)
	}
}

// installEnvDumpingBinary puts an executable called name first on PATH. When
// run, it writes its environment to the returned file and prints a version.
func installEnvDumpingBinary(t *testing.T, name string) string {
	t.Helper()
	binDir := t.TempDir()
	dump := filepath.Join(t.TempDir(), "env.txt")
	script := "#!/bin/sh\nenv > '" + dump + "'\necho 1.0.0\n"
	if err := os.WriteFile(filepath.Join(binDir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dump
}

// readEnvDump returns the environment a fake binary recorded, failing the
// test when the binary never ran.
func readEnvDump(t *testing.T, dump string) string {
	t.Helper()
	got, err := os.ReadFile(dump)
	if err != nil {
		t.Fatalf("the probe never ran the binary: %v", err)
	}
	return string(got)
}

// The --version probe runs the same binary the agent launch does, so it must
// not hand that binary keys TRACKER_STRIP_ACP_KEYS=1 keeps from the agent.
func TestACPVersionProbeHonorsStripACPKeys(t *testing.T) {
	dump := installEnvDumpingBinary(t, "tracker-test-acp-agent")
	t.Setenv("TRACKER_STRIP_ACP_KEYS", "1")
	t.Setenv("ANTHROPIC_API_KEY", "sentinel-provider-key")

	if _, err := NewACPBackend().ensureAgentPath("tracker-test-acp-agent"); err != nil {
		t.Fatalf("ensureAgentPath: %v", err)
	}
	if env := readEnvDump(t, dump); strings.Contains(env, "sentinel-provider-key") {
		t.Error("the --version probe saw ANTHROPIC_API_KEY although TRACKER_STRIP_ACP_KEYS=1 strips it from the agent")
	}
}

// TRACKER_STRIP_ACP_KEYS=1 is the operator's stricter stance for everything
// the ACP backend starts. The terminal's credential filter must not weaken
// it: the eleven names stay out even when TRACKER_PASS_ENV=1 lifts the
// pattern filter, as they did before #660 moved terminals to exec.CommandEnv.
func TestACPTerminalHonorsStripACPKeys(t *testing.T) {
	for _, passEnv := range []string{"", "1"} {
		t.Run("TRACKER_PASS_ENV="+passEnv, func(t *testing.T) {
			t.Setenv("TRACKER_STRIP_ACP_KEYS", "1")
			t.Setenv("TRACKER_PASS_ENV", passEnv)
			t.Setenv("ANTHROPIC_API_KEY", "sentinel-provider-key")
			t.Setenv("OPENAI_BASE_URL", "https://sentinel-gateway.invalid")

			out := runACPTerminal(t, acp.CreateTerminalRequest{
				Command: "sh",
				Args:    []string{"-c", `printf '%s|%s' "${ANTHROPIC_API_KEY-unset}" "${OPENAI_BASE_URL-unset}"`},
			})
			if out != "unset|unset" {
				t.Errorf("output = %q, want both names stripped under TRACKER_STRIP_ACP_KEYS=1", out)
			}
		})
	}
}
