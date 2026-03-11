package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/2389-research/tracker/pipeline/handlers"
	"github.com/2389-research/tracker/tui"
)

func TestChooseInterviewerReturnsBubbleteaWhenTerminal(t *testing.T) {
	iv := chooseInterviewer(true)
	if _, ok := iv.(*tui.BubbleteaInterviewer); !ok {
		t.Errorf("expected *tui.BubbleteaInterviewer when terminal, got %T", iv)
	}
}

func TestChooseInterviewerReturnsConsoleWhenNotTerminal(t *testing.T) {
	iv := chooseInterviewer(false)
	if _, ok := iv.(*handlers.ConsoleInterviewer); !ok {
		t.Errorf("expected *handlers.ConsoleInterviewer when not terminal, got %T", iv)
	}
}

func TestParseFlagsEnablesVerbose(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "--verbose", "pipe.dot"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.mode != modeRun {
		t.Fatalf("mode = %q, want %q", cfg.mode, modeRun)
	}
	if !cfg.verbose {
		t.Fatal("expected verbose to be true")
	}
	if cfg.dotFile != "pipe.dot" {
		t.Fatalf("dotFile = %q, want %q", cfg.dotFile, "pipe.dot")
	}
}

func TestParseFlagsFlagsAfterDotFile(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "pipeline.dot", "-c", "checkpoint.json"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.mode != modeRun {
		t.Fatalf("mode = %q, want %q", cfg.mode, modeRun)
	}
	if cfg.dotFile != "pipeline.dot" {
		t.Fatalf("dotFile = %q, want %q", cfg.dotFile, "pipeline.dot")
	}
	if cfg.checkpoint != "checkpoint.json" {
		t.Fatalf("checkpoint = %q, want %q", cfg.checkpoint, "checkpoint.json")
	}
}

func TestParseFlagsFlagsBeforeDotFile(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "-c", "checkpoint.json", "pipeline.dot"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.mode != modeRun {
		t.Fatalf("mode = %q, want %q", cfg.mode, modeRun)
	}
	if cfg.dotFile != "pipeline.dot" {
		t.Fatalf("dotFile = %q, want %q", cfg.dotFile, "pipeline.dot")
	}
	if cfg.checkpoint != "checkpoint.json" {
		t.Fatalf("checkpoint = %q, want %q", cfg.checkpoint, "checkpoint.json")
	}
}

func TestParseFlagsMixedOrder(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "--no-tui", "pipeline.dot", "-c", "cp.json", "--verbose"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.mode != modeRun {
		t.Fatalf("mode = %q, want %q", cfg.mode, modeRun)
	}
	if cfg.dotFile != "pipeline.dot" {
		t.Fatalf("dotFile = %q, want %q", cfg.dotFile, "pipeline.dot")
	}
	if cfg.checkpoint != "cp.json" {
		t.Fatalf("checkpoint = %q, want %q", cfg.checkpoint, "cp.json")
	}
	if !cfg.noTUI {
		t.Fatal("expected noTUI to be true")
	}
	if !cfg.verbose {
		t.Fatal("expected verbose to be true")
	}
}

func TestParseFlagsDefaultIsTUI(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "pipeline.dot"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.mode != modeRun {
		t.Fatalf("mode = %q, want %q", cfg.mode, modeRun)
	}
	if cfg.noTUI {
		t.Fatal("expected noTUI to be false by default (TUI is the default)")
	}
}

func TestParseFlagsSetupMode(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "setup"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.mode != modeSetup {
		t.Fatalf("mode = %q, want %q", cfg.mode, modeSetup)
	}
	if cfg.dotFile != "" {
		t.Fatalf("dotFile = %q, want empty", cfg.dotFile)
	}
}

func TestLoadEnvFilesLoadsXDGThenLocal(t *testing.T) {
	workdir := t.TempDir()
	configHome := t.TempDir()

	localEnv := filepath.Join(workdir, ".env")
	if err := os.WriteFile(localEnv, []byte("OPENAI_API_KEY=local-openai\nANTHROPIC_API_KEY=local-anthropic\n"), 0o600); err != nil {
		t.Fatalf("write local .env: %v", err)
	}

	configDir := filepath.Join(configHome, "tracker")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configEnv := filepath.Join(configDir, ".env")
	if err := os.WriteFile(configEnv, []byte("OPENAI_API_KEY=xdg-openai\nGEMINI_API_KEY=xdg-gemini\n"), 0o600); err != nil {
		t.Fatalf("write config .env: %v", err)
	}

	t.Setenv("XDG_CONFIG_HOME", configHome)
	unsetEnvForTest(t, "OPENAI_API_KEY")
	unsetEnvForTest(t, "ANTHROPIC_API_KEY")
	unsetEnvForTest(t, "GEMINI_API_KEY")

	if err := loadEnvFiles(workdir); err != nil {
		t.Fatalf("loadEnvFiles returned error: %v", err)
	}

	if got := os.Getenv("OPENAI_API_KEY"); got != "local-openai" {
		t.Fatalf("OPENAI_API_KEY = %q, want %q", got, "local-openai")
	}
	if got := os.Getenv("ANTHROPIC_API_KEY"); got != "local-anthropic" {
		t.Fatalf("ANTHROPIC_API_KEY = %q, want %q", got, "local-anthropic")
	}
	if got := os.Getenv("GEMINI_API_KEY"); got != "xdg-gemini" {
		t.Fatalf("GEMINI_API_KEY = %q, want %q", got, "xdg-gemini")
	}
}

func TestLoadEnvFilesDoesNotOverrideShellEnv(t *testing.T) {
	workdir := t.TempDir()
	configHome := t.TempDir()

	if err := os.WriteFile(filepath.Join(workdir, ".env"), []byte("OPENAI_API_KEY=local-openai\n"), 0o600); err != nil {
		t.Fatalf("write local .env: %v", err)
	}
	configDir := filepath.Join(configHome, "tracker")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, ".env"), []byte("OPENAI_API_KEY=xdg-openai\n"), 0o600); err != nil {
		t.Fatalf("write config .env: %v", err)
	}

	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("OPENAI_API_KEY", "shell-openai")

	if err := loadEnvFiles(workdir); err != nil {
		t.Fatalf("loadEnvFiles returned error: %v", err)
	}

	if got := os.Getenv("OPENAI_API_KEY"); got != "shell-openai" {
		t.Fatalf("OPENAI_API_KEY = %q, want %q", got, "shell-openai")
	}
}

func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()

	oldValue, existed := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unsetenv %s: %v", key, err)
	}
	t.Cleanup(func() {
		var err error
		if existed {
			err = os.Setenv(key, oldValue)
		} else {
			err = os.Unsetenv(key)
		}
		if err != nil {
			t.Fatalf("restore env %s: %v", key, err)
		}
	})
}
