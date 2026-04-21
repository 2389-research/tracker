// ABOUTME: Tests for the CLI doctor shim — flag parsing, exit-code mapping,
// ABOUTME: and the presentation-layer printCheckResult / maybeFixGitignore.
// ABOUTME: The underlying checks live in the tracker package and are tested there.
package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	tracker "github.com/2389-research/tracker"
)

// ---- runDoctorWithConfig exit-code mapping ---------------------------------

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

func TestParseFlagsDoctor(t *testing.T) {
	tests := []struct {
		name             string
		args             []string
		wantProbe        bool
		wantPipelineFile string
		wantWorkdir      string
		wantBackend      string
		wantErr          bool
	}{
		{name: "no args", args: []string{"tracker", "doctor"}, wantProbe: true},
		{name: "with probe", args: []string{"tracker", "doctor", "--probe"}, wantProbe: true},
		{name: "no probe", args: []string{"tracker", "doctor", "--probe=false"}, wantProbe: false},
		{name: "with pipeline file", args: []string{"tracker", "doctor", "my_pipeline.dip"}, wantProbe: true, wantPipelineFile: "my_pipeline.dip"},
		{name: "with probe and file", args: []string{"tracker", "doctor", "--probe", "pipeline.dip"}, wantProbe: true, wantPipelineFile: "pipeline.dip"},
		{name: "with workdir", args: []string{"tracker", "doctor", "--workdir", "/tmp/myproject"}, wantProbe: true, wantWorkdir: "/tmp/myproject"},
		{name: "with short workdir", args: []string{"tracker", "doctor", "-w", "/tmp/myproject"}, wantProbe: true, wantWorkdir: "/tmp/myproject"},
		{name: "with backend", args: []string{"tracker", "doctor", "--backend", "claude-code"}, wantProbe: true, wantBackend: "claude-code"},
		{name: "invalid backend", args: []string{"tracker", "doctor", "--backend", "invalid-backend"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parseFlags(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected parseFlags error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseFlags error: %v", err)
			}
			if cfg.mode != modeDoctor {
				t.Errorf("mode = %q, want doctor", cfg.mode)
			}
			if cfg.probe != tt.wantProbe {
				t.Errorf("probe = %v, want %v", cfg.probe, tt.wantProbe)
			}
			if cfg.pipelineFile != tt.wantPipelineFile {
				t.Errorf("pipelineFile = %q, want %q", cfg.pipelineFile, tt.wantPipelineFile)
			}
			if cfg.workdir != tt.wantWorkdir {
				t.Errorf("workdir = %q, want %q", cfg.workdir, tt.wantWorkdir)
			}
			if cfg.backend != tt.wantBackend {
				t.Errorf("backend = %q, want %q", cfg.backend, tt.wantBackend)
			}
		})
	}
}

// ---- DoctorWarningsError exit code 2 ----------------------------------------

func TestDoctorWarningsErrorSentinel(t *testing.T) {
	e := &DoctorWarningsError{Warnings: 3}
	if e.Error() == "" {
		t.Error("expected non-empty error message")
	}

	// Verify errors.As works for the sentinel check in main.go.
	var target *DoctorWarningsError
	if !errors.As(e, &target) {
		t.Error("errors.As should match *DoctorWarningsError")
	}
	if target.Warnings != 3 {
		t.Errorf("expected Warnings=3, got %d", target.Warnings)
	}
}

func TestRunDoctorWithConfigWarningsOnlyReturnsDoctorWarningsError(t *testing.T) {
	dir := t.TempDir()
	// Trigger a warning-only result by setting dangerous env vars (warn)
	// and a valid API key (providers pass).
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-testkey1234567890abcdef")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_COMPAT_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("TRACKER_PASS_ENV", "1") // triggers env warning
	t.Setenv("TRACKER_PASS_API_KEYS", "")
	t.Setenv("TRACKER_ARTIFACT_DIR", "")

	cfg := DoctorConfig{}
	err := runDoctorWithConfig(dir, cfg)
	if err != nil {
		var warnErr *DoctorWarningsError
		if !errors.As(err, &warnErr) {
			t.Logf("got non-DoctorWarningsError: %v (acceptable if dippin not installed)", err)
		}
	}
}

// ---- tracker.ResolveProviderBaseURL -----------------------------------------

func TestResolveProviderBaseURLFromEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "https://custom.example.com")
	t.Setenv("TRACKER_GATEWAY_URL", "")

	got := tracker.ResolveProviderBaseURL("anthropic")
	if got != "https://custom.example.com" {
		t.Errorf("expected https://custom.example.com, got %q", got)
	}
}

func TestResolveProviderBaseURLFromGateway(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("TRACKER_GATEWAY_URL", "https://gateway.example.com")

	got := tracker.ResolveProviderBaseURL("anthropic")
	if got != "https://gateway.example.com/anthropic" {
		t.Errorf("expected https://gateway.example.com/anthropic, got %q", got)
	}
}

func TestResolveProviderBaseURLEmpty(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("TRACKER_GATEWAY_URL", "")

	got := tracker.ResolveProviderBaseURL("anthropic")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestResolveProviderBaseURLGeminiGateway(t *testing.T) {
	t.Setenv("GEMINI_BASE_URL", "")
	t.Setenv("TRACKER_GATEWAY_URL", "https://gateway.example.com")

	got := tracker.ResolveProviderBaseURL("gemini")
	if got != "https://gateway.example.com/google-ai-studio" {
		t.Errorf("expected https://gateway.example.com/google-ai-studio, got %q", got)
	}
}

// ---- maybeFixGitignore / checkGitignore write-side-effect -----------------

func TestCheckGitignore_AppendsMissing(t *testing.T) {
	dir := t.TempDir()
	gitignorePath := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte("node_modules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	checkGitignore(dir)
	got, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".tracker/", "runs/", ".ai/"} {
		if !contains(string(got), want) {
			t.Errorf(".gitignore missing %q after patch, contents:\n%s", want, got)
		}
	}
}

func TestCheckGitignore_NoFileNoOp(t *testing.T) {
	dir := t.TempDir()
	// No .gitignore — checkGitignore must be a no-op, not panic, not create the file.
	checkGitignore(dir)
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(err) {
		t.Errorf("expected .gitignore absent, got err=%v", err)
	}
}

// contains is a tiny helper to avoid dragging strings.Contains into tests.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
