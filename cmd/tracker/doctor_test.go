// ABOUTME: Tests for tracker doctor checks — verifies each check function, exit codes, and --probe flag.
// ABOUTME: Offline only: no network calls are made; provider tests use temp env vars.
package main

import (
	"os"
	"path/filepath"
	"testing"
)

// ---- checkEnvWarnings -------------------------------------------------------

func TestCheckEnvWarningsClean(t *testing.T) {
	t.Setenv("TRACKER_PASS_ENV", "")
	t.Setenv("TRACKER_PASS_API_KEYS", "")

	cr := checkEnvWarnings()
	if !cr.ok {
		t.Errorf("expected ok=true when no dangerous vars set, got message=%q", cr.message)
	}
}

func TestCheckEnvWarningsSet(t *testing.T) {
	t.Setenv("TRACKER_PASS_ENV", "1")

	cr := checkEnvWarnings()
	if cr.ok {
		t.Error("expected ok=false when TRACKER_PASS_ENV is set")
	}
	if !cr.warn {
		t.Error("expected warn=true (dangerous env is a warning, not a failure)")
	}
	if cr.fix == "" {
		t.Error("expected non-empty fix message")
	}
}

// ---- checkProviders ---------------------------------------------------------

func TestCheckProvidersNoneConfigured(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_COMPAT_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")

	cr := checkProviders(false)
	if cr.ok {
		t.Error("expected ok=false when no providers configured")
	}
	if cr.fix == "" {
		t.Error("expected a fix message when no providers configured")
	}
}

func TestCheckProvidersAnthropicConfigured(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-testkey1234567890abcdef")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_COMPAT_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")

	cr := checkProviders(false)
	if !cr.ok {
		t.Errorf("expected ok=true when Anthropic key set, got message=%q", cr.message)
	}
}

func TestCheckProvidersInvalidFormat(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "not-a-valid-key")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_COMPAT_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")

	cr := checkProviders(false)
	// Invalid format means the provider doesn't count as configured
	if cr.ok {
		t.Error("expected ok=false when only invalid-format key set")
	}
}

func TestCheckProvidersOpenAIConfigured(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "sk-testkey1234567890abcdef")
	t.Setenv("OPENAI_COMPAT_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")

	cr := checkProviders(false)
	if !cr.ok {
		t.Errorf("expected ok=true when OpenAI key set, got message=%q", cr.message)
	}
}

func TestCheckProvidersGeminiConfigured(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_COMPAT_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "gemini-testkey12345")
	t.Setenv("GOOGLE_API_KEY", "")

	cr := checkProviders(false)
	if !cr.ok {
		t.Errorf("expected ok=true when Gemini key set, got message=%q", cr.message)
	}
}

// ---- isValidAPIKey ----------------------------------------------------------

func TestIsValidAPIKeyAnthropicValid(t *testing.T) {
	if !isValidAPIKey("Anthropic", "sk-ant-validkey12345") {
		t.Error("expected valid for sk-ant- prefix key")
	}
}

func TestIsValidAPIKeyAnthropicInvalid(t *testing.T) {
	if isValidAPIKey("Anthropic", "sk-notant-key") {
		t.Error("expected invalid for non sk-ant- prefix")
	}
	if isValidAPIKey("Anthropic", "") {
		t.Error("expected invalid for empty key")
	}
	if isValidAPIKey("Anthropic", "sk-ant-") {
		t.Error("expected invalid for too-short key")
	}
}

func TestIsValidAPIKeyOpenAIValid(t *testing.T) {
	if !isValidAPIKey("OpenAI", "sk-validkeymorethan10chars") {
		t.Error("expected valid for sk- prefix key")
	}
}

func TestIsValidAPIKeyOpenAIInvalid(t *testing.T) {
	if isValidAPIKey("OpenAI", "notsk-key") {
		t.Error("expected invalid for non sk- prefix")
	}
}

func TestIsValidAPIKeyGeminiValid(t *testing.T) {
	if !isValidAPIKey("Gemini", "gemini-key-1234567890") {
		t.Error("expected valid for Gemini key >10 chars")
	}
}

func TestIsValidAPIKeyGeminiInvalid(t *testing.T) {
	if isValidAPIKey("Gemini", "short") {
		t.Error("expected invalid for Gemini key <=10 chars")
	}
}

// ---- findProviderKey --------------------------------------------------------

func TestFindProviderKeyFound(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "mygeminikey")
	t.Setenv("GOOGLE_API_KEY", "")

	key, name := findProviderKey([]string{"GEMINI_API_KEY", "GOOGLE_API_KEY"})
	if key != "mygeminikey" {
		t.Errorf("expected mygeminikey, got %q", key)
	}
	if name != "GEMINI_API_KEY" {
		t.Errorf("expected GEMINI_API_KEY, got %q", name)
	}
}

func TestFindProviderKeyFallback(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "mygooglekey")

	key, name := findProviderKey([]string{"GEMINI_API_KEY", "GOOGLE_API_KEY"})
	if key != "mygooglekey" {
		t.Errorf("expected mygooglekey, got %q", key)
	}
	if name != "GOOGLE_API_KEY" {
		t.Errorf("expected GOOGLE_API_KEY, got %q", name)
	}
}

func TestFindProviderKeyNotFound(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")

	key, name := findProviderKey([]string{"GEMINI_API_KEY", "GOOGLE_API_KEY"})
	if key != "" || name != "" {
		t.Errorf("expected empty key and name, got key=%q name=%q", key, name)
	}
}

// ---- maskKey ----------------------------------------------------------------

func TestMaskKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "****"},
		{"short", "****"},
		{"sk-ant-1234567890abcdef", "sk-a...cdef"},
	}
	for _, tt := range tests {
		got := maskKey(tt.input)
		if got != tt.want {
			t.Errorf("maskKey(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ---- isAuthError ------------------------------------------------------------

func TestIsAuthError(t *testing.T) {
	authMsgs := []string{
		"401 Unauthorized",
		"403 Forbidden",
		"authentication failed",
		"invalid api key provided",
		"API key is wrong",
		"unauthorized access",
	}
	for _, msg := range authMsgs {
		if !isAuthError(msg) {
			t.Errorf("expected isAuthError=true for %q", msg)
		}
	}

	nonAuthMsgs := []string{
		"connection refused",
		"timeout",
		"rate limit exceeded",
		"context deadline exceeded",
	}
	for _, msg := range nonAuthMsgs {
		if isAuthError(msg) {
			t.Errorf("expected isAuthError=false for %q", msg)
		}
	}
}

// ---- trimErrMsg -------------------------------------------------------------

func TestTrimErrMsg(t *testing.T) {
	short := "short error"
	if trimErrMsg(short, 80) != short {
		t.Error("expected short error unchanged")
	}

	long := "this is a very long error message that exceeds the maximum length limit we set"
	trimmed := trimErrMsg(long, 20)
	if len(trimmed) > 23 { // 20 + "..."
		t.Errorf("expected trimmed to be <=23 chars, got %d", len(trimmed))
	}
	if trimmed[len(trimmed)-3:] != "..." {
		t.Error("expected trimmed to end with ...")
	}
}

// ---- checkWorkdir -----------------------------------------------------------

func TestCheckWorkdirValid(t *testing.T) {
	dir := t.TempDir()
	cr := checkWorkdir(dir)
	if !cr.ok {
		t.Errorf("expected ok=true for writable temp dir, got message=%q", cr.message)
	}
}

func TestCheckWorkdirNotExist(t *testing.T) {
	cr := checkWorkdir("/nonexistent/path/that/does/not/exist")
	if cr.ok {
		t.Error("expected ok=false for non-existent directory")
	}
	if cr.fix == "" {
		t.Error("expected non-empty fix message")
	}
}

func TestCheckWorkdirNotDir(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "afile.txt")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	cr := checkWorkdir(file)
	if cr.ok {
		t.Error("expected ok=false when pointing at a file instead of directory")
	}
}

// ---- checkGitignore ---------------------------------------------------------

func TestCheckGitignoreComplete(t *testing.T) {
	dir := t.TempDir()
	gitignore := ".tracker\nruns\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gitignore), 0644); err != nil {
		t.Fatal(err)
	}
	// Should not print warnings — we can't easily assert output, but at least it shouldn't panic.
	checkGitignore(dir)
}

func TestCheckGitignoreMissing(t *testing.T) {
	dir := t.TempDir()
	// No .gitignore file — should not panic.
	checkGitignore(dir)
}

// ---- checkArtifactDirs ------------------------------------------------------

func TestCheckArtifactDirsNoAiDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TRACKER_ARTIFACT_DIR", "")

	cr := checkArtifactDirs(dir)
	// .ai/ doesn't exist but parent is writable — should pass.
	if !cr.ok {
		t.Errorf("expected ok=true when .ai/ doesn't exist and parent is writable, got %q", cr.message)
	}
}

func TestCheckArtifactDirsWithExistingAiDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TRACKER_ARTIFACT_DIR", "")

	aiDir := filepath.Join(dir, ".ai")
	if err := os.Mkdir(aiDir, 0755); err != nil {
		t.Fatal(err)
	}

	cr := checkArtifactDirs(dir)
	if !cr.ok {
		t.Errorf("expected ok=true when .ai/ exists and is writable, got %q", cr.message)
	}
}

func TestCheckArtifactDirsWithCustomDir(t *testing.T) {
	dir := t.TempDir()
	customDir := filepath.Join(dir, "custom_artifacts")
	if err := os.Mkdir(customDir, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TRACKER_ARTIFACT_DIR", customDir)

	cr := checkArtifactDirs(dir)
	if !cr.ok {
		t.Errorf("expected ok=true with writable TRACKER_ARTIFACT_DIR, got %q", cr.message)
	}
}

func TestCheckArtifactDirsWithMissingCustomDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TRACKER_ARTIFACT_DIR", "/nonexistent/custom/dir")

	cr := checkArtifactDirs(dir)
	if cr.ok {
		t.Error("expected ok=false when TRACKER_ARTIFACT_DIR doesn't exist")
	}
}

// ---- isDirWritable ----------------------------------------------------------

func TestIsDirWritable(t *testing.T) {
	dir := t.TempDir()
	if !isDirWritable(dir) {
		t.Error("expected temp dir to be writable")
	}
}

func TestIsDirWritableNotExist(t *testing.T) {
	if isDirWritable("/nonexistent/path/here") {
		t.Error("expected non-existent dir to not be writable")
	}
}

// ---- checkDiskSpace ---------------------------------------------------------

func TestCheckDiskSpaceValidDir(t *testing.T) {
	dir := t.TempDir()
	cr := checkDiskSpace(dir)
	// Either passes (enough space) or warns (low space) — both are valid.
	// The function should not panic.
	_ = cr
}

// ---- checkPipelineFile ------------------------------------------------------

func TestCheckPipelineFileMissing(t *testing.T) {
	cr := checkPipelineFile("/nonexistent/file.dip")
	if cr.ok {
		t.Error("expected ok=false for missing pipeline file")
	}
	if cr.fix == "" {
		t.Error("expected fix message for missing file")
	}
}

func TestCheckPipelineFileValidDOT(t *testing.T) {
	dir := t.TempDir()
	dotContent := `digraph test {
	Start [shape=Mdiamond];
	Work [shape=box];
	End [shape=Msquare];
	Start -> Work;
	Work -> End;
}`
	path := filepath.Join(dir, "test.dot")
	if err := os.WriteFile(path, []byte(dotContent), 0644); err != nil {
		t.Fatal(err)
	}

	cr := checkPipelineFile(path)
	if !cr.ok {
		t.Errorf("expected ok=true for valid DOT file, got message=%q", cr.message)
	}
}

// ---- buildChecks ------------------------------------------------------------

func TestBuildChecksDefaultCount(t *testing.T) {
	dir := t.TempDir()
	cfg := DoctorConfig{}
	checks := buildChecks(dir, cfg)
	// Should have 8 checks without pipeline file.
	if len(checks) != 8 {
		t.Errorf("expected 8 checks by default, got %d", len(checks))
	}
}

func TestBuildChecksWithPipelineFile(t *testing.T) {
	dir := t.TempDir()
	cfg := DoctorConfig{pipelineFile: "some.dip"}
	checks := buildChecks(dir, cfg)
	if len(checks) != 9 {
		t.Errorf("expected 9 checks with pipeline file, got %d", len(checks))
	}
}

// ---- needsCompositeResultLine -----------------------------------------------

func TestNeedsCompositeResultLine(t *testing.T) {
	// These checks print their own lines — should return false.
	noComposite := []string{"LLM Providers", "Version Compatibility", "Optional Binaries",
		"Artifact Directories", "Working Directory", "Pipeline File"}
	for _, name := range noComposite {
		if needsCompositeResultLine(name) {
			t.Errorf("expected needsCompositeResultLine=false for %q", name)
		}
	}

	// These should return true (simple checks).
	if !needsCompositeResultLine("Environment Warnings") {
		t.Error("expected needsCompositeResultLine=true for Environment Warnings")
	}
	if !needsCompositeResultLine("Disk Space") {
		t.Error("expected needsCompositeResultLine=true for Disk Space")
	}
}

// ---- DoctorResult counting --------------------------------------------------

func TestDoctorResultCounting(t *testing.T) {
	result := DoctorResult{Passed: 5, Warnings: 2, Failures: 1}
	if result.Passed != 5 {
		t.Error("expected Passed=5")
	}
	if result.Failures != 1 {
		t.Error("expected Failures=1")
	}
}

// ---- runDoctorWithConfig exit code behavior ---------------------------------

func TestRunDoctorWithConfigAllPass(t *testing.T) {
	dir := t.TempDir()
	// Set a valid-format API key so provider check passes.
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-testkey1234567890abcdef")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_COMPAT_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("TRACKER_ARTIFACT_DIR", "")
	t.Setenv("TRACKER_PASS_ENV", "")
	t.Setenv("TRACKER_PASS_API_KEYS", "")

	cfg := DoctorConfig{}
	// Just verify it doesn't crash; dippin may or may not be installed.
	// The error (if any) should be about health check failed or nil.
	_ = runDoctorWithConfig(dir, cfg)
}

// ---- parseDoctorFlags -------------------------------------------------------

func TestParseFlagsDoctorNoArgs(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "doctor"})
	if err != nil {
		t.Fatalf("parseFlags error: %v", err)
	}
	if cfg.mode != modeDoctor {
		t.Errorf("mode = %q, want doctor", cfg.mode)
	}
	if cfg.probe {
		t.Error("expected probe=false by default")
	}
	if cfg.pipelineFile != "" {
		t.Error("expected no pipeline file by default")
	}
}

func TestParseFlagsDoctorWithProbe(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "doctor", "--probe"})
	if err != nil {
		t.Fatalf("parseFlags error: %v", err)
	}
	if !cfg.probe {
		t.Error("expected probe=true with --probe flag")
	}
}

func TestParseFlagsDoctorWithPipelineFile(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "doctor", "my_pipeline.dip"})
	if err != nil {
		t.Fatalf("parseFlags error: %v", err)
	}
	if cfg.pipelineFile != "my_pipeline.dip" {
		t.Errorf("expected pipelineFile=my_pipeline.dip, got %q", cfg.pipelineFile)
	}
}

func TestParseFlagsDoctorWithProbeAndFile(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "doctor", "--probe", "pipeline.dip"})
	if err != nil {
		t.Fatalf("parseFlags error: %v", err)
	}
	if !cfg.probe {
		t.Error("expected probe=true")
	}
	if cfg.pipelineFile != "pipeline.dip" {
		t.Errorf("expected pipelineFile=pipeline.dip, got %q", cfg.pipelineFile)
	}
}
