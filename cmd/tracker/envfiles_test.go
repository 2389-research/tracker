// ABOUTME: Black-box tests for the source-aware .env loader (#659): a project .env may supply only provider
// ABOUTME: keys, the config .env may add base URLs and knobs, everything else is shell-only and refused loudly.
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	execpkg "github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/internal/envpolicy"
)

// envFixture is a temp XDG config home plus a temp workdir with the loader's
// stderr captured. These tests mutate the process environment and must never
// run in parallel.
type envFixture struct {
	workdir    string
	configHome string
	stderr     *bytes.Buffer
}

func newEnvFixture(t *testing.T) *envFixture {
	t.Helper()
	f := &envFixture{workdir: t.TempDir(), configHome: t.TempDir(), stderr: &bytes.Buffer{}}
	t.Setenv("XDG_CONFIG_HOME", f.configHome)
	t.Setenv("TRACKER_ENV_FILES", "")
	prev := envFilesStderr
	envFilesStderr = f.stderr
	t.Cleanup(func() { envFilesStderr = prev })
	envpolicy.ResetProvenance()
	t.Cleanup(envpolicy.ResetProvenance)
	return f
}

func (f *envFixture) projectEnvPath() string { return filepath.Join(f.workdir, ".env") }
func (f *envFixture) configEnvPath() string {
	return filepath.Join(f.configHome, "tracker", ".env")
}

func (f *envFixture) writeProject(t *testing.T, content string) {
	t.Helper()
	writeEnvFixture(t, f.projectEnvPath(), content)
}

func (f *envFixture) writeConfig(t *testing.T, content string) {
	t.Helper()
	writeEnvFixture(t, f.configEnvPath(), content)
}

func writeEnvFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func (f *envFixture) load(t *testing.T, flagMode string) {
	t.Helper()
	if err := loadEnvFilesMode(f.workdir, flagMode); err != nil {
		t.Fatalf("loadEnvFilesMode: %v\nstderr:\n%s", err, f.stderr.String())
	}
}

// shellOnlyNames is every registered shell-only variable (explicit and
// implicit) plus one member of each implicit prefix family. XDG_CONFIG_HOME
// is left out only because the fixture itself relies on it to locate the
// config file; it is registered shell-only like the rest and takes the same
// refusal path (TestSecurityPurposesAreShellOnly covers the registration).
func shellOnlyNames() []string {
	var names []string
	for _, v := range envpolicy.Table() {
		if v.Source == envpolicy.ShellOnly && v.Name != "XDG_CONFIG_HOME" {
			names = append(names, v.Name)
		}
	}
	return append(names, "DYLD_INSERT_LIBRARIES", "GIT_CONFIG_KEY_0", "LD_AUDIT")
}

func TestProjectEnvCannotSetAnyShellOnlyVar(t *testing.T) {
	f := newEnvFixture(t)
	names := shellOnlyNames()
	var content strings.Builder
	for _, n := range names {
		unsetEnvForTest(t, n)
		content.WriteString(n + "=injected-by-project-env\n")
	}
	f.writeProject(t, content.String())

	f.load(t, "")

	for _, n := range names {
		if got := os.Getenv(n); got != "" {
			t.Errorf("%s = %q after loading a project .env; a project file must not set a shell-only variable", n, got)
		}
		if !strings.Contains(f.stderr.String(), n) {
			t.Errorf("stderr does not name refused key %s", n)
		}
	}
	if !strings.Contains(f.stderr.String(), f.projectEnvPath()) {
		t.Errorf("stderr does not name the offending file %s:\n%s", f.projectEnvPath(), f.stderr.String())
	}
	if !strings.Contains(f.stderr.String(), "shell") {
		t.Errorf("stderr does not say where the keys belong (shell):\n%s", f.stderr.String())
	}
}

func TestConfigEnvCannotFlipSecuritySwitches(t *testing.T) {
	f := newEnvFixture(t)
	switches := []string{"TRACKER_PASS_ENV", "TRACKER_PASS_API_KEYS", "TRACKER_STRIP_ACP_KEYS", "TRACKER_FAIL_ON_OVERRIDE", "TRACKER_ENV_FILES", "TRACKER_AUDIT_DIR", "HERDR_BIN_PATH", "HERDR_ENV"}
	var content strings.Builder
	for _, n := range switches {
		unsetEnvForTest(t, n)
		content.WriteString(n + "=1\n")
	}
	unsetEnvForTest(t, "OPENAI_BASE_URL")
	content.WriteString("OPENAI_BASE_URL=https://proxy.example.internal/v1\n")
	f.writeConfig(t, content.String())

	f.load(t, "")

	for _, n := range switches {
		if got := os.Getenv(n); got != "" {
			t.Errorf("%s = %q after loading the config .env; security switches are shell-only", n, got)
		}
		if !strings.Contains(f.stderr.String(), n) {
			t.Errorf("stderr does not name refused key %s", n)
		}
	}
	if got := os.Getenv("OPENAI_BASE_URL"); got != "https://proxy.example.internal/v1" {
		t.Errorf("OPENAI_BASE_URL = %q; the config file may set base URLs", got)
	}
}

func TestProjectEnvOverridesConfigOnlyForCredentials(t *testing.T) {
	f := newEnvFixture(t)
	for _, n := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_BASE_URL", "TRACKER_GATEWAY_URL", "TRACKER_NO_NOTIFY"} {
		unsetEnvForTest(t, n)
	}
	f.writeConfig(t, "OPENAI_API_KEY=cfg-key\nOPENAI_BASE_URL=https://cfg.example/v1\nTRACKER_GATEWAY_URL=https://gw.example\n")
	f.writeProject(t, "OPENAI_API_KEY=proj-key\nANTHROPIC_API_KEY=proj-anth\nOPENAI_BASE_URL=https://evil.example\nTRACKER_GATEWAY_URL=https://evil.example\nTRACKER_NO_NOTIFY=1\n")

	f.load(t, "")

	want := map[string]string{
		"OPENAI_API_KEY":      "proj-key",
		"ANTHROPIC_API_KEY":   "proj-anth",
		"OPENAI_BASE_URL":     "https://cfg.example/v1",
		"TRACKER_GATEWAY_URL": "https://gw.example",
		"TRACKER_NO_NOTIFY":   "",
	}
	for n, w := range want {
		if got := os.Getenv(n); got != w {
			t.Errorf("%s = %q, want %q", n, got, w)
		}
	}
	for _, n := range []string{"OPENAI_BASE_URL", "TRACKER_GATEWAY_URL", "TRACKER_NO_NOTIFY"} {
		if !strings.Contains(f.stderr.String(), n) {
			t.Errorf("stderr does not name refused project key %s:\n%s", n, f.stderr.String())
		}
	}
	if got := envpolicy.AppliedFrom("OPENAI_API_KEY"); got != f.projectEnvPath() {
		t.Errorf("provenance OPENAI_API_KEY = %q, want project file %s", got, f.projectEnvPath())
	}
	if got := envpolicy.AppliedFrom("OPENAI_BASE_URL"); got != f.configEnvPath() {
		t.Errorf("provenance OPENAI_BASE_URL = %q, want config file %s", got, f.configEnvPath())
	}
	var skippedURL bool
	for _, o := range envpolicy.Skipped() {
		if o.Name == "OPENAI_BASE_URL" && o.File == f.projectEnvPath() {
			skippedURL = true
		}
	}
	if !skippedURL {
		t.Errorf("provenance has no skipped record for OPENAI_BASE_URL from %s: %+v", f.projectEnvPath(), envpolicy.Skipped())
	}
}

func TestProjectEnvUnregisteredNameIsSkipped(t *testing.T) {
	f := newEnvFixture(t)
	unsetEnvForTest(t, "TOTALLY_UNKNOWN_KNOB")
	f.writeProject(t, "TOTALLY_UNKNOWN_KNOB=1\n")
	f.load(t, "")
	if got := os.Getenv("TOTALLY_UNKNOWN_KNOB"); got != "" {
		t.Errorf("unregistered name was applied: %q", got)
	}
	if !strings.Contains(f.stderr.String(), "TOTALLY_UNKNOWN_KNOB") || !strings.Contains(f.stderr.String(), "shell") {
		t.Errorf("stderr should name the key and point to the shell:\n%s", f.stderr.String())
	}
}

func TestProjectEnvShellStillWinsSilently(t *testing.T) {
	f := newEnvFixture(t)
	t.Setenv("OPENAI_API_KEY", "shell-key")
	f.writeProject(t, "OPENAI_API_KEY=proj-key\n")
	f.load(t, "")
	if got := os.Getenv("OPENAI_API_KEY"); got != "shell-key" {
		t.Errorf("OPENAI_API_KEY = %q, want the shell value", got)
	}
	if f.stderr.Len() != 0 {
		t.Errorf("a shell-wins skip must stay silent, got:\n%s", f.stderr.String())
	}
}

func TestProjectEnvSymlinkIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	f := newEnvFixture(t)
	unsetEnvForTest(t, "OPENAI_API_KEY")
	real := filepath.Join(t.TempDir(), "real.env")
	writeEnvFixture(t, real, "OPENAI_API_KEY=via-symlink\n")
	if err := os.Symlink(real, f.projectEnvPath()); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	f.load(t, "")
	if got := os.Getenv("OPENAI_API_KEY"); got != "" {
		t.Errorf("a symlinked project .env was loaded (OPENAI_API_KEY=%q)", got)
	}
	if !strings.Contains(f.stderr.String(), "symlink") {
		t.Errorf("stderr should explain the symlink refusal:\n%s", f.stderr.String())
	}
}

func TestProjectEnvWorldWritableIsSkipped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful on Windows")
	}
	for _, mode := range []os.FileMode{0o666, 0o660} {
		f := newEnvFixture(t)
		unsetEnvForTest(t, "OPENAI_API_KEY")
		f.writeProject(t, "OPENAI_API_KEY=loose-perms\n")
		if err := os.Chmod(f.projectEnvPath(), mode); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		f.load(t, "")
		if got := os.Getenv("OPENAI_API_KEY"); got != "" {
			t.Errorf("mode %o: group/world-writable project .env was loaded (OPENAI_API_KEY=%q)", mode, got)
		}
		if !strings.Contains(f.stderr.String(), "writable") {
			t.Errorf("mode %o: stderr should explain the permission refusal:\n%s", mode, f.stderr.String())
		}
	}
}

func TestConfigEnvWorldWritableIsSkipped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful on Windows")
	}
	for _, mode := range []os.FileMode{0o666, 0o660} {
		f := newEnvFixture(t)
		unsetEnvForTest(t, "TRACKER_GATEWAY_URL")
		f.writeConfig(t, "TRACKER_GATEWAY_URL=https://attacker.example\n")
		if err := os.Chmod(f.configEnvPath(), mode); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		f.load(t, "")
		if got := os.Getenv("TRACKER_GATEWAY_URL"); got != "" {
			t.Errorf("mode %o: group/world-writable config .env was loaded (TRACKER_GATEWAY_URL=%q)", mode, got)
		}
		if !strings.Contains(f.stderr.String(), "writable") {
			t.Errorf("mode %o: stderr should explain the permission refusal:\n%s", mode, f.stderr.String())
		}
	}
}

func TestConfigEnvWorldReadableStillLoads(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful on Windows")
	}
	// 0644 is world-READABLE, not writable — it must still load.
	for _, mode := range []os.FileMode{0o600, 0o644} {
		f := newEnvFixture(t)
		unsetEnvForTest(t, "TRACKER_GATEWAY_URL")
		f.writeConfig(t, "TRACKER_GATEWAY_URL=https://gw.example\n")
		if err := os.Chmod(f.configEnvPath(), mode); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		f.load(t, "")
		if got := os.Getenv("TRACKER_GATEWAY_URL"); got != "https://gw.example" {
			t.Errorf("mode %o: config .env did not load (TRACKER_GATEWAY_URL=%q)", mode, got)
		}
	}
}

func TestEnvFilesModeSelectsFiles(t *testing.T) {
	cases := []struct {
		name, flag, env      string
		wantCfg, wantProject bool
	}{
		{"default all", "", "", true, true},
		{"flag config", "config", "", true, false},
		{"flag none", "none", "", false, false},
		{"env config", "", "config", true, false},
		{"env none", "", "none", false, false},
		{"flag beats env", "all", "none", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newEnvFixture(t)
			t.Setenv("TRACKER_ENV_FILES", tc.env)
			unsetEnvForTest(t, "GEMINI_API_KEY")
			unsetEnvForTest(t, "OPENAI_API_KEY")
			f.writeConfig(t, "GEMINI_API_KEY=cfg\n")
			f.writeProject(t, "OPENAI_API_KEY=proj\n")
			f.load(t, tc.flag)
			if got := os.Getenv("GEMINI_API_KEY") != ""; got != tc.wantCfg {
				t.Errorf("config loaded = %v, want %v", got, tc.wantCfg)
			}
			if got := os.Getenv("OPENAI_API_KEY") != ""; got != tc.wantProject {
				t.Errorf("project loaded = %v, want %v", got, tc.wantProject)
			}
		})
	}
}

func TestEnvFilesModeRejectsInvalidValue(t *testing.T) {
	f := newEnvFixture(t)
	if err := loadEnvFilesMode(f.workdir, "sometimes"); err == nil {
		t.Error("--env-files=sometimes accepted, want error")
	}
	t.Setenv("TRACKER_ENV_FILES", "maybe")
	if err := loadEnvFilesMode(f.workdir, ""); err == nil {
		t.Error("TRACKER_ENV_FILES=maybe accepted, want error")
	}
}

func TestVersionDoesNotLoadProjectEnv(t *testing.T) {
	f := newEnvFixture(t)
	unsetEnvForTest(t, "OPENAI_API_KEY")
	unsetEnvForTest(t, "GEMINI_API_KEY")
	f.writeProject(t, "OPENAI_API_KEY=proj\n")
	f.writeConfig(t, "GEMINI_API_KEY=cfg\n")
	t.Chdir(f.workdir)
	if err := executeVersion(runConfig{}); err != nil {
		t.Fatalf("executeVersion: %v", err)
	}
	if got := os.Getenv("OPENAI_API_KEY"); got != "" {
		t.Errorf("tracker version loaded the cwd project .env (OPENAI_API_KEY=%q)", got)
	}
	if got := os.Getenv("GEMINI_API_KEY"); got != "cfg" {
		t.Errorf("tracker version did not load the config .env (GEMINI_API_KEY=%q)", got)
	}
}

func TestCommandEnvIgnoresPassEnvFromProjectFile(t *testing.T) {
	f := newEnvFixture(t)
	t.Setenv("ANTHROPIC_API_KEY", "shell-secret")
	unsetEnvForTest(t, "TRACKER_PASS_ENV")
	f.writeProject(t, "TRACKER_PASS_ENV=1\n")
	f.load(t, "")
	for _, entry := range execpkg.CommandEnv() {
		if strings.HasPrefix(entry, "ANTHROPIC_API_KEY=") {
			t.Fatalf("CommandEnv leaked ANTHROPIC_API_KEY after a project .env set TRACKER_PASS_ENV=1")
		}
	}
}

func TestXDGConfigHomeRelativeIsIgnored(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "relative/config")
	path, err := resolveConfigEnvPath()
	if err != nil {
		t.Fatalf("resolveConfigEnvPath: %v", err)
	}
	if want := filepath.Join(home, ".config", "tracker", ".env"); path != want {
		t.Errorf("config env path = %q, want %q (relative XDG_CONFIG_HOME must be ignored)", path, want)
	}
}

func TestEnvFilesFlagParses(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "--env-files", "config", "pipeline.dip"})
	if err != nil || cfg.envFiles != "config" {
		t.Errorf("run: envFiles = %q, err = %v; want config", cfg.envFiles, err)
	}
	if _, err := parseFlags([]string{"tracker", "--env-files=sometimes", "pipeline.dip"}); err == nil {
		t.Error("run: --env-files=sometimes accepted, want parse error")
	}
	cfg, err = parseFlags([]string{"tracker", "doctor", "--env-files=none"})
	if err != nil || cfg.envFiles != "none" {
		t.Errorf("doctor: envFiles = %q, err = %v; want none", cfg.envFiles, err)
	}
	if _, err := parseFlags([]string{"tracker", "doctor", "--env-files=sometimes"}); err == nil {
		t.Error("doctor: --env-files=sometimes accepted, want parse error")
	}
	cfg, err = parseFlags([]string{"tracker", "pipeline.dip"})
	if err != nil || cfg.envFiles != "" {
		t.Errorf("default: envFiles = %q, err = %v; want empty (defer to TRACKER_ENV_FILES, then all)", cfg.envFiles, err)
	}
}

func TestProviderEnvKeysDelegateToPolicy(t *testing.T) {
	for _, n := range envpolicy.ProviderKeyVars() {
		if _, ok := providerEnvKeys[n]; !ok {
			t.Errorf("providerEnvKeys lacks %s from envpolicy.ProviderKeyVars", n)
		}
	}
	for _, n := range envpolicy.ProviderBaseURLVars() {
		if _, ok := providerEnvKeys[n]; !ok {
			t.Errorf("providerEnvKeys lacks %s from envpolicy.ProviderBaseURLVars", n)
		}
	}
}
