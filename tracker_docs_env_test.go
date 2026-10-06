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

// tableRow returns the <tr> of the cli.html env table whose FIRST cell names
// envVar (alone or in a "A / B / C" combined row), or "" when no row exists.
// Matching the first cell only means a stray mention in prose or in another
// row's description never satisfies a "has a row" assertion.
var tableRowRe = regexp.MustCompile(`(?s)<tr><td>(.*?)</td>.*?</tr>`)

func tableRow(html, envVar string) string {
	needle := "<code>" + envVar + "</code>"
	for _, m := range tableRowRe.FindAllStringSubmatch(html, -1) {
		if strings.Contains(m[1], needle) {
			return m[0]
		}
	}
	return ""
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
		if tableRow(html, name) == "" {
			t.Errorf("cli.html env tables: %s is read by the code but has no row", name)
		}
	}

	// cmd/tracker/commands.go: only executeRun (deps.loadEnv), executeVersion
	// and executeDoctor call loadEnvFiles; setup.go writes the file. Every other
	// subcommand (diagnose, validate, simulate, estimate, audit, status,
	// run-json, workflows, init, verify-tests, update) never reads it.
	xdgRow := tableRow(html, "XDG_CONFIG_HOME")
	if strings.Contains(xdgRow, "every command") {
		t.Errorf("cli.html XDG_CONFIG_HOME row says every command reads the config .env; only run/doctor/version call loadEnvFiles (cmd/tracker/commands.go):\n%s", xdgRow)
	}
	for _, cmd := range []string{"tracker run", "tracker doctor", "tracker version", "tracker setup"} {
		if !strings.Contains(xdgRow, cmd) {
			t.Errorf("cli.html XDG_CONFIG_HOME row must name %q as a reader/writer of the config .env:\n%s", cmd, xdgRow)
		}
	}

	// llm/openaicompat/adapter.go defaultBaseURL = https://openrouter.ai/api.
	if row := tableRow(html, "OPENAI_COMPAT_BASE_URL"); !strings.Contains(row, "openrouter.ai") {
		t.Errorf("cli.html OPENAI_COMPAT_BASE_URL row must state the unset default (OpenRouter, llm/openaicompat/adapter.go defaultBaseURL):\n%s", row)
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
	// pipeline/handlers/tool.go buildToolEnv and pipeline/git_artifacts.go
	// gitSafeEnv filter under EVERY backend, not just native; the native
	// column must not file tool nodes under the native backend's children.
	if !strings.Contains(envRow, "every backend") {
		t.Errorf("cli.html backend comparison: Environment row must say tool nodes and git subprocesses are filtered under every backend (tool.go buildToolEnv, git_artifacts.go gitSafeEnv):\n%s", envRow)
	}

	// pipeline/handlers/backend_acp.go estimateACPUsage -> llm.EstimateCostForProvider
	// prices cache-read tokens at the model's catalog rate (gpt-4.1 0.25x /
	// gpt-4o 0.5x via overlayCacheMultipliers' fallback only for gap models);
	// the 10% figure is just llm/pricing.go defaultCacheReadMultiplier. The row
	// must not present a fixed 10% as the cache-read rate.
	ratioRow := tableRow(html, "TRACKER_ACP_CACHE_READ_RATIO")
	if ratioRow == "" {
		t.Fatal("cli.html: no TRACKER_ACP_CACHE_READ_RATIO row")
	}
	if !strings.Contains(ratioRow, "model-specific") {
		t.Errorf("cli.html TRACKER_ACP_CACHE_READ_RATIO row must describe the cache-read rate as model-specific (llm.EstimateCostForProvider uses catalog rates), with 10%% only a fallback:\n%s", ratioRow)
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
	// exec.CommandEnv is also every tool node's env (pipeline/handlers/tool.go
	// buildToolEnv) and gitSafeEnv applies the same patterns to every git
	// subprocess (pipeline/git_artifacts.go) — under claude-code and acp too.
	if strings.Contains(strings.ToLower(section), "only the native backend") {
		t.Error("CLAUDE.md 'Agent backends' claims exec.CommandEnv is native-only; tool nodes (tool.go buildToolEnv) and git (gitSafeEnv) are filtered under every backend")
	}
	if !strings.Contains(section, "every backend") {
		t.Error("CLAUDE.md 'Agent backends' must say tool nodes and git subprocesses are credential-filtered under every backend")
	}

	backends := readDoc(t, "docs/architecture/backends.md")
	if !strings.Contains(backends, "CommandEnv") {
		t.Error("docs/architecture/backends.md: comparison must describe the native backend's exec.CommandEnv filter (v0.77.0)")
	}
	if strings.Contains(backends, "only applies to the native backend") {
		t.Error("docs/architecture/backends.md claims exec.CommandEnv only applies to the native backend's children; tool nodes and git are filtered under every backend")
	}
	if !strings.Contains(backends, "every backend") {
		t.Error("docs/architecture/backends.md must say tool nodes and git subprocesses are credential-filtered under every backend")
	}
	// agent/turn_checkpoint.go captureWorkTreeSHA runs `git rev-parse` with no
	// cmd.Env (inherited, unfiltered), so backends.md must not present gitSafeEnv
	// as covering every git subprocess — it must name the unfiltered probe.
	if !strings.Contains(backends, "captureWorkTreeSHA") {
		t.Error("docs/architecture/backends.md must note the turn_checkpoint captureWorkTreeSHA git call is NOT gitSafeEnv-filtered (agent/turn_checkpoint.go runs git rev-parse with an inherited environment)")
	}

	codergen := readDoc(t, "docs/architecture/handlers/codergen.md")
	if strings.Contains(codergen, "§ Claude Code backend") {
		t.Error("docs/architecture/handlers/codergen.md points to a CLAUDE.md section 'Claude Code backend' that does not exist; the section is 'Agent backends'")
	}
	if !strings.Contains(codergen, "five provider key") {
		t.Error("docs/architecture/handlers/codergen.md must qualify the claude-code strip as the five provider keys (backend_claudecode.go buildEnv), not 'API keys' unqualified")
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

	// The website changelog mirrors CHANGELOG.md; the 0.16.0 line there must
	// carry the same note so the two changelogs agree.
	site := readDoc(t, "site/content/changelog.html")
	li := regexp.MustCompile(`(?s)<li><strong>ACP environment scoping[^<]*</strong>.*?</li>`).FindString(site)
	if li == "" {
		t.Fatal("site/content/changelog.html: no 0.16.0 'ACP environment scoping' entry")
	}
	if !strings.Contains(li, "TRACKER_STRIP_ACP_KEYS") {
		t.Errorf("site/content/changelog.html 0.16.0 entry lacks the TRACKER_STRIP_ACP_KEYS editor's note that CHANGELOG.md carries:\n%s", li)
	}
}
