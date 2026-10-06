// ABOUTME: Pins the environment-variable prose in the shipped docs to the code that reads each variable.
// ABOUTME: Guards the #660 drift class: a backend env switch described for the wrong backend, or not at all.
package tracker

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// readDoc reads a repo-relative doc; the root package's test dir is the repo root.
func readDoc(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// tableRow returns the <tr> of the cli.html env table whose first cell names
// envVar, or "" when no row exists.
func tableRow(html, envVar string) string {
	re := regexp.MustCompile(`(?s)<tr><td><code>` + regexp.QuoteMeta(envVar) + `</code>.*?</tr>`)
	return re.FindString(html)
}

// TestCLIEnvTableMatchesBackendEnvPolicies cross-checks the website's env
// table against the switches the code actually reads:
//   - TRACKER_PASS_API_KEYS is read only by the claude-code backend
//     (pipeline/handlers/backend_claudecode.go buildEnv).
//   - TRACKER_STRIP_ACP_KEYS is the ACP switch (backend_acp.go buildEnvForACP),
//     full passthrough by default.
//   - the native backend's children get exec.CommandEnv since v0.77.0.
func TestCLIEnvTableMatchesBackendEnvPolicies(t *testing.T) {
	html := readDoc(t, "site/content/cli.html")

	passRow := tableRow(html, "TRACKER_PASS_API_KEYS")
	if passRow == "" {
		t.Fatal("cli.html: no TRACKER_PASS_API_KEYS row")
	}
	if strings.Contains(passRow, "--backend acp") {
		t.Errorf("cli.html TRACKER_PASS_API_KEYS row claims it applies to --backend acp; backend_acp.go reads TRACKER_STRIP_ACP_KEYS instead:\n%s", passRow)
	}
	if !strings.Contains(passRow, "v0.13.0") {
		t.Errorf("cli.html TRACKER_PASS_API_KEYS row badge should be v0.13.0 (CHANGELOG 0.13.0 'Claude-code env: API keys stripped'):\n%s", passRow)
	}

	if tableRow(html, "TRACKER_STRIP_ACP_KEYS") == "" {
		t.Error("cli.html: no TRACKER_STRIP_ACP_KEYS row (read by pipeline/handlers/backend_acp.go buildEnvForACP)")
	}

	for _, name := range []string{
		"TRACKER_AUDIT_DIR", "XDG_STATE_HOME", "XDG_CONFIG_HOME",
		"OPENAI_COMPAT_API_KEY", "OPENAI_COMPAT_BASE_URL",
		"HERDR_ENV", "HERDR_PANE_ID", "HERDR_BIN_PATH", "TRACKER_HERDR",
		"TRACKER_DEBUG", "TRACKER_NO_NOTIFY", "TRACKER_NO_UPDATE_CHECK",
		"TRACKER_ACP_CACHE_READ_RATIO", "TRACKER_CODEGEN_PROVIDER", "TRACKER_SPRINT_WRITER_PROVIDER",
	} {
		if !strings.Contains(html, "<code>"+name+"</code>") {
			t.Errorf("cli.html env tables: %s is read by the code but has no row", name)
		}
	}

	envRow := regexp.MustCompile(`(?s)<tr><td><strong>Environment</strong></td>.*?</tr>`).FindString(html)
	if envRow == "" {
		t.Fatal("cli.html: backend comparison has no Environment row")
	}
	if strings.Contains(envRow, "Full process environment") {
		t.Errorf("cli.html backend comparison: native column says 'Full process environment'; since v0.77.0 it is exec.CommandEnv-filtered:\n%s", envRow)
	}
	if !strings.Contains(envRow, "TRACKER_STRIP_ACP_KEYS") {
		t.Errorf("cli.html backend comparison: ACP column must name TRACKER_STRIP_ACP_KEYS:\n%s", envRow)
	}
}

// TestREADMEConfigEnvPath pins the XDG config path in the README to
// cmd/tracker/config_env.go resolveConfigEnvPath (<XDG_CONFIG_HOME>/tracker/.env).
func TestREADMEConfigEnvPath(t *testing.T) {
	readme := readDoc(t, "README.md")
	if strings.Contains(readme, "2389/tracker/.env") {
		t.Error("README.md names ~/.config/2389/tracker/.env; config_env.go writes ~/.config/tracker/.env")
	}
	if !strings.Contains(readme, "~/.config/tracker/.env") {
		t.Error("README.md should name ~/.config/tracker/.env")
	}
}

// TestBackendDocsStateACPEnvDefault pins the ACP env default in the project
// docs to backend_acp.go buildEnvForACP: passthrough unless TRACKER_STRIP_ACP_KEYS=1.
func TestBackendDocsStateACPEnvDefault(t *testing.T) {
	claudeMD := readDoc(t, "CLAUDE.md")
	start := strings.Index(claudeMD, "### Agent backends")
	if start < 0 {
		t.Fatal("CLAUDE.md: no '### Agent backends' section")
	}
	section := claudeMD[start:]
	if end := strings.Index(section[1:], "\n### "); end > 0 {
		section = section[:end+1]
	}
	if !strings.Contains(section, "TRACKER_STRIP_ACP_KEYS") {
		t.Error("CLAUDE.md 'Agent backends' describes claude-code's TRACKER_PASS_API_KEYS but not ACP's TRACKER_STRIP_ACP_KEYS")
	}

	backends := readDoc(t, "docs/architecture/backends.md")
	if !strings.Contains(backends, "CommandEnv") {
		t.Error("docs/architecture/backends.md: comparison must describe the native backend's exec.CommandEnv filter (v0.77.0)")
	}

	changelog := readDoc(t, "CHANGELOG.md")
	idx := strings.Index(changelog, "## [0.16.0]")
	if idx < 0 {
		t.Fatal("CHANGELOG.md: no 0.16.0 entry")
	}
	entry := changelog[idx:]
	if end := strings.Index(entry[1:], "\n## ["); end > 0 {
		entry = entry[:end+1]
	}
	if !strings.Contains(entry, "TRACKER_STRIP_ACP_KEYS") {
		t.Error("CHANGELOG.md 0.16.0 'ACP environment scoping' still describes strip-by-default; 6f8ff7b (v0.17.0) flipped it to passthrough with TRACKER_STRIP_ACP_KEYS and needs an editor's note")
	}
}
