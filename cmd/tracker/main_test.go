package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
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
	cfg, err := parseFlags([]string{"tracker", "pipeline.dot", "-r", "abc123"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.mode != modeRun {
		t.Fatalf("mode = %q, want %q", cfg.mode, modeRun)
	}
	if cfg.dotFile != "pipeline.dot" {
		t.Fatalf("dotFile = %q, want %q", cfg.dotFile, "pipeline.dot")
	}
	if cfg.resumeID != "abc123" {
		t.Fatalf("resumeID = %q, want %q", cfg.resumeID, "abc123")
	}
}

func TestParseFlagsFlagsBeforeDotFile(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "-r", "abc123", "pipeline.dot"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.mode != modeRun {
		t.Fatalf("mode = %q, want %q", cfg.mode, modeRun)
	}
	if cfg.dotFile != "pipeline.dot" {
		t.Fatalf("dotFile = %q, want %q", cfg.dotFile, "pipeline.dot")
	}
	if cfg.resumeID != "abc123" {
		t.Fatalf("resumeID = %q, want %q", cfg.resumeID, "abc123")
	}
}

func TestParseFlagsMixedOrder(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "--no-tui", "pipeline.dot", "-r", "run42", "--verbose"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.mode != modeRun {
		t.Fatalf("mode = %q, want %q", cfg.mode, modeRun)
	}
	if cfg.dotFile != "pipeline.dot" {
		t.Fatalf("dotFile = %q, want %q", cfg.dotFile, "pipeline.dot")
	}
	if cfg.resumeID != "run42" {
		t.Fatalf("resumeID = %q, want %q", cfg.resumeID, "run42")
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

func TestExecuteCommandRoutesSetupMode(t *testing.T) {
	var setupCalled bool

	err := executeCommand(runConfig{mode: modeSetup}, commandDeps{
		runSetup: func() error {
			setupCalled = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeCommand returned error: %v", err)
	}
	if !setupCalled {
		t.Fatal("expected setup command to be invoked")
	}
}

func TestExecuteCommandRunModeUsesRunPath(t *testing.T) {
	var loadEnvCalled bool
	var runCalled bool

	err := executeCommand(runConfig{
		mode:    modeRun,
		dotFile: "pipeline.dot",
		workdir: "/tmp/workdir",
		noTUI:   true,
	}, commandDeps{
		loadEnv: func(workdir string) error {
			loadEnvCalled = true
			if workdir != "/tmp/workdir" {
				t.Fatalf("loadEnv workdir = %q, want %q", workdir, "/tmp/workdir")
			}
			return nil
		},
		run: func(dotFile, workdir, checkpoint string, verbose bool, jsonOut bool) error {
			runCalled = true
			if dotFile != "pipeline.dot" {
				t.Fatalf("dotFile = %q, want %q", dotFile, "pipeline.dot")
			}
			return nil
		},
		runTUI: func(dotFile, workdir, checkpoint string, verbose bool) error {
			t.Fatal("did not expect TUI path")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeCommand returned error: %v", err)
	}
	if !loadEnvCalled {
		t.Fatal("expected env loading before run mode")
	}
	if !runCalled {
		t.Fatal("expected non-TUI run path")
	}
}

func TestRunSetupCommandSavesUpdatedKeys(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	configPath := filepath.Join(configHome, "tracker", ".env")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("OPENAI_API_KEY=old-openai\nEXTRA_FLAG=keep-me\n"), 0o600); err != nil {
		t.Fatalf("write existing env file: %v", err)
	}

	err := runSetupCommand(func(existing map[string]string) (setupResult, error) {
		if existing["OPENAI_API_KEY"] != "old-openai" {
			t.Fatalf("existing OPENAI_API_KEY = %q, want %q", existing["OPENAI_API_KEY"], "old-openai")
		}
		return setupResult{
			values: map[string]string{
				"OPENAI_API_KEY":    "new-openai",
				"GEMINI_API_KEY":    "new-gemini",
				"UNRELATED_ENV_VAR": "ignored",
			},
		}, nil
	})
	if err != nil {
		t.Fatalf("runSetupCommand returned error: %v", err)
	}

	values, err := readEnvFile(configPath)
	if err != nil {
		t.Fatalf("readEnvFile returned error: %v", err)
	}
	if values["OPENAI_API_KEY"] != "new-openai" {
		t.Fatalf("OPENAI_API_KEY = %q, want %q", values["OPENAI_API_KEY"], "new-openai")
	}
	if values["GEMINI_API_KEY"] != "new-gemini" {
		t.Fatalf("GEMINI_API_KEY = %q, want %q", values["GEMINI_API_KEY"], "new-gemini")
	}
	if values["EXTRA_FLAG"] != "keep-me" {
		t.Fatalf("EXTRA_FLAG = %q, want %q", values["EXTRA_FLAG"], "keep-me")
	}
	if _, exists := values["UNRELATED_ENV_VAR"]; exists {
		t.Fatal("did not expect unrelated ui values to be written")
	}
}

func TestRunSetupCommandCancelLeavesFileUntouched(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	configPath := filepath.Join(configHome, "tracker", ".env")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	original := []byte("OPENAI_API_KEY=old-openai\nEXTRA_FLAG=keep-me\n")
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatalf("write existing env file: %v", err)
	}

	err := runSetupCommand(func(existing map[string]string) (setupResult, error) {
		return setupResult{cancelled: true}, nil
	})
	if err != nil {
		t.Fatalf("runSetupCommand returned error: %v", err)
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	if string(content) != string(original) {
		t.Fatalf("config file changed on cancel: got %q want %q", string(content), string(original))
	}
}

func TestPrintRunSummaryShowsResumeHintOnIncompleteRun(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	result := &pipeline.EngineResult{
		RunID:  "abc123",
		Status: pipeline.OutcomeFail,
	}
	printRunSummary(result, fmt.Errorf("interrupted"), nil, "my_pipeline.dot")

	w.Close()
	os.Stdout = old

	var buf [4096]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "Resume") {
		t.Fatalf("expected Resume section in output, got:\n%s", output)
	}
	if !strings.Contains(output, "tracker -r abc123 my_pipeline.dot") {
		t.Fatalf("expected resume command with run ID in output, got:\n%s", output)
	}
}

func TestPrintRunSummaryNoResumeOnSuccess(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	result := &pipeline.EngineResult{
		RunID:  "abc123",
		Status: pipeline.OutcomeSuccess,
	}
	printRunSummary(result, nil, nil, "my_pipeline.dot")

	w.Close()
	os.Stdout = old

	var buf [4096]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if strings.Contains(output, "Resume") {
		t.Fatalf("did not expect Resume section on successful run, got:\n%s", output)
	}
}

func TestResolveCheckpointExactMatch(t *testing.T) {
	workdir := t.TempDir()
	runsDir := filepath.Join(workdir, ".tracker", "runs", "abc123def456")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cpPath := filepath.Join(runsDir, "checkpoint.json")
	if err := os.WriteFile(cpPath, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write checkpoint: %v", err)
	}

	got, err := resolveCheckpoint(workdir, "abc123def456")
	if err != nil {
		t.Fatalf("resolveCheckpoint returned error: %v", err)
	}
	if got != cpPath {
		t.Fatalf("got %q, want %q", got, cpPath)
	}
}

func TestResolveCheckpointPrefixMatch(t *testing.T) {
	workdir := t.TempDir()
	runsDir := filepath.Join(workdir, ".tracker", "runs", "abc123def456")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cpPath := filepath.Join(runsDir, "checkpoint.json")
	if err := os.WriteFile(cpPath, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write checkpoint: %v", err)
	}

	got, err := resolveCheckpoint(workdir, "abc123")
	if err != nil {
		t.Fatalf("resolveCheckpoint returned error: %v", err)
	}
	if got != cpPath {
		t.Fatalf("got %q, want %q", got, cpPath)
	}
}

func TestResolveCheckpointAmbiguous(t *testing.T) {
	workdir := t.TempDir()
	base := filepath.Join(workdir, ".tracker", "runs")
	for _, id := range []string{"abc123aaa", "abc123bbb"} {
		dir := filepath.Join(base, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "checkpoint.json"), []byte(`{}`), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	_, err := resolveCheckpoint(workdir, "abc123")
	if err == nil {
		t.Fatal("expected error for ambiguous prefix")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous error, got: %v", err)
	}
}

func TestResolveCheckpointNotFound(t *testing.T) {
	workdir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workdir, ".tracker", "runs"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, err := resolveCheckpoint(workdir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing run")
	}
	if !strings.Contains(err.Error(), "no run found") {
		t.Fatalf("expected 'no run found' error, got: %v", err)
	}
}

func TestResolveCheckpointMissingCheckpointFile(t *testing.T) {
	workdir := t.TempDir()
	// Run directory exists but has no checkpoint.json inside.
	runDir := filepath.Join(workdir, ".tracker", "runs", "abc123def456")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, err := resolveCheckpoint(workdir, "abc123def456")
	if err == nil {
		t.Fatal("expected error for missing checkpoint file")
	}
	if !strings.Contains(err.Error(), "checkpoint not found") {
		t.Fatalf("expected 'checkpoint not found' error, got: %v", err)
	}
}

func TestResolveCheckpointAmbiguousWithExactMatch(t *testing.T) {
	workdir := t.TempDir()
	base := filepath.Join(workdir, ".tracker", "runs")
	// Two dirs: "abc123" (exact) and "abc123def" (prefix match).
	for _, id := range []string{"abc123", "abc123def"} {
		dir := filepath.Join(base, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "checkpoint.json"), []byte(`{}`), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	got, err := resolveCheckpoint(workdir, "abc123")
	if err != nil {
		t.Fatalf("expected exact match to resolve ambiguity, got error: %v", err)
	}
	want := filepath.Join(base, "abc123", "checkpoint.json")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestParseFlagsJsonFlag(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "--json", "pipeline.dot"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if !cfg.jsonOut {
		t.Fatal("expected jsonOut to be true")
	}
	if cfg.dotFile != "pipeline.dot" {
		t.Fatalf("dotFile = %q, want %q", cfg.dotFile, "pipeline.dot")
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
