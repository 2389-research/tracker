package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/pipeline/handlers"
	"github.com/2389-research/tracker/tui"
)

func floatNear(a, b, epsilon float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < epsilon
}

func TestChooseInterviewerReturnsBubbleteaWhenTerminal(t *testing.T) {
	iv := chooseInterviewer(true, autopilotCfg{}, nil, "")
	if _, ok := iv.(*tui.BubbleteaInterviewer); !ok {
		t.Errorf("expected *tui.BubbleteaInterviewer when terminal, got %T", iv)
	}
}

func TestChooseInterviewerReturnsConsoleWhenNotTerminal(t *testing.T) {
	iv := chooseInterviewer(false, autopilotCfg{}, nil, "")
	if _, ok := iv.(*handlers.ConsoleInterviewer); !ok {
		t.Errorf("expected *handlers.ConsoleInterviewer when not terminal, got %T", iv)
	}
}

func TestChooseInterviewerAutoApprove(t *testing.T) {
	iv := chooseInterviewer(true, autopilotCfg{autoApprove: true}, nil, "")
	if _, ok := iv.(*handlers.AutoApproveFreeformInterviewer); !ok {
		t.Errorf("expected *handlers.AutoApproveFreeformInterviewer, got %T", iv)
	}
}

func TestChooseInterviewerAutopilotNoClientFallsBack(t *testing.T) {
	// When backend is not claude-code and llmClient is nil, should fall back to auto-approve.
	iv := chooseInterviewer(true, autopilotCfg{persona: "lax"}, nil, "")
	if _, ok := iv.(*handlers.AutoApproveFreeformInterviewer); !ok {
		t.Errorf("expected auto-approve fallback with nil client, got %T", iv)
	}
}

func TestChooseInterviewerClaudeCodeBackend(t *testing.T) {
	// When backend is claude-code and autopilot is active, should try claude-code autopilot.
	// Will fall back since claude binary may not be in test PATH, but shouldn't panic.
	iv := chooseInterviewer(true, autopilotCfg{persona: "lax"}, nil, "claude-code")
	if iv == nil {
		t.Error("expected non-nil interviewer")
	}
}

func TestParseFlagsAutopilot(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "--autopilot", "hard", "pipeline.dip"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.autopilot != "hard" {
		t.Fatalf("autopilot = %q, want %q", cfg.autopilot, "hard")
	}
}

func TestParseFlagsAutoApprove(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "--auto-approve", "pipeline.dip"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if !cfg.autoApprove {
		t.Fatal("expected autoApprove to be true")
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
	if cfg.pipelineFile != "pipe.dot" {
		t.Fatalf("dotFile = %q, want %q", cfg.pipelineFile, "pipe.dot")
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
	if cfg.pipelineFile != "pipeline.dot" {
		t.Fatalf("dotFile = %q, want %q", cfg.pipelineFile, "pipeline.dot")
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
	if cfg.pipelineFile != "pipeline.dot" {
		t.Fatalf("dotFile = %q, want %q", cfg.pipelineFile, "pipeline.dot")
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
	if cfg.pipelineFile != "pipeline.dot" {
		t.Fatalf("dotFile = %q, want %q", cfg.pipelineFile, "pipeline.dot")
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
	if cfg.pipelineFile != "" {
		t.Fatalf("dotFile = %q, want empty", cfg.pipelineFile)
	}
}

func TestParseFlagsFormatFlag(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "--format", "dot", "pipeline.dip"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.format != "dot" {
		t.Fatalf("format = %q, want %q", cfg.format, "dot")
	}
	if cfg.pipelineFile != "pipeline.dip" {
		t.Fatalf("pipelineFile = %q, want %q", cfg.pipelineFile, "pipeline.dip")
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
		mode:         modeRun,
		pipelineFile: "pipeline.dot",
		workdir:      "/tmp/workdir",
		noTUI:        true,
	}, commandDeps{
		loadEnv: func(workdir string) error {
			loadEnvCalled = true
			if workdir != "/tmp/workdir" {
				t.Fatalf("loadEnv workdir = %q, want %q", workdir, "/tmp/workdir")
			}
			return nil
		},
		run: func(pipelineFile, workdir, checkpoint, format, backend string, verbose bool, jsonOut bool) error {
			runCalled = true
			if pipelineFile != "pipeline.dot" {
				t.Fatalf("pipelineFile = %q, want %q", pipelineFile, "pipeline.dot")
			}
			return nil
		},
		runTUI: func(pipelineFile, workdir, checkpoint, format, backend string, verbose bool) error {
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
	printRunSummary(result, fmt.Errorf("interrupted"), "my_pipeline.dot")

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
	printRunSummary(result, nil, "my_pipeline.dot")

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
	if cfg.pipelineFile != "pipeline.dot" {
		t.Fatalf("dotFile = %q, want %q", cfg.pipelineFile, "pipeline.dot")
	}
}

func TestAggregateSessionStatsEmpty(t *testing.T) {
	entries := []pipeline.TraceEntry{
		{NodeID: "s", HandlerName: "start", Status: "success"},
		{NodeID: "end", HandlerName: "exit", Status: "success"},
	}
	agg := aggregateSessionStats(entries)
	if agg.TotalTurns != 0 {
		t.Errorf("expected 0 turns, got %d", agg.TotalTurns)
	}
	if agg.TotalToolCalls != 0 {
		t.Errorf("expected 0 tool calls, got %d", agg.TotalToolCalls)
	}
	if agg.Compactions != 0 {
		t.Errorf("expected 0 compactions, got %d", agg.Compactions)
	}
	if len(agg.FilesCreated) != 0 {
		t.Errorf("expected 0 files created, got %d", len(agg.FilesCreated))
	}
}

func TestAggregateSessionStatsMultipleNodes(t *testing.T) {
	entries := []pipeline.TraceEntry{
		{NodeID: "s", HandlerName: "start", Status: "success"},
		{
			NodeID: "impl1", HandlerName: "codergen", Status: "success",
			Stats: &pipeline.SessionStats{
				Turns:          10,
				TotalToolCalls: 50,
				ToolCalls:      map[string]int{"bash": 30, "write": 20},
				FilesCreated:   []string{"a.go", "b.go"},
				FilesModified:  []string{"main.go"},
				Compactions:    1,
				InputTokens:    5000,
				OutputTokens:   2000,
				TotalTokens:    7000,
				CostUSD:        0.10,
			},
		},
		{
			NodeID: "impl2", HandlerName: "codergen", Status: "success",
			Stats: &pipeline.SessionStats{
				Turns:          5,
				TotalToolCalls: 25,
				ToolCalls:      map[string]int{"bash": 10, "read": 15},
				FilesCreated:   []string{"c.go", "a.go"}, // a.go is a duplicate
				FilesModified:  []string{"main.go", "util.go"},
				Compactions:    2,
				InputTokens:    3000,
				OutputTokens:   1000,
				TotalTokens:    4000,
				CostUSD:        0.06,
			},
		},
		{NodeID: "end", HandlerName: "exit", Status: "success"},
	}

	agg := aggregateSessionStats(entries)

	if agg.TotalTurns != 15 {
		t.Errorf("expected 15 turns, got %d", agg.TotalTurns)
	}
	if agg.TotalToolCalls != 75 {
		t.Errorf("expected 75 tool calls, got %d", agg.TotalToolCalls)
	}
	if agg.ToolCallsByName["bash"] != 40 {
		t.Errorf("expected bash=40, got %d", agg.ToolCallsByName["bash"])
	}
	if agg.ToolCallsByName["write"] != 20 {
		t.Errorf("expected write=20, got %d", agg.ToolCallsByName["write"])
	}
	if agg.ToolCallsByName["read"] != 15 {
		t.Errorf("expected read=15, got %d", agg.ToolCallsByName["read"])
	}
	if agg.Compactions != 3 {
		t.Errorf("expected 3 compactions, got %d", agg.Compactions)
	}
	// Deduplication: a.go appears in both, should appear once
	if len(agg.FilesCreated) != 3 {
		t.Errorf("expected 3 unique created files, got %d: %v", len(agg.FilesCreated), agg.FilesCreated)
	}
	// main.go appears in both modified lists, should appear once
	if len(agg.FilesModified) != 2 {
		t.Errorf("expected 2 unique modified files, got %d: %v", len(agg.FilesModified), agg.FilesModified)
	}
	// Token aggregation
	if agg.TotalInputTokens != 8000 {
		t.Errorf("expected TotalInputTokens=8000, got %d", agg.TotalInputTokens)
	}
	if agg.TotalOutputTokens != 3000 {
		t.Errorf("expected TotalOutputTokens=3000, got %d", agg.TotalOutputTokens)
	}
	if agg.TotalTokens != 11000 {
		t.Errorf("expected TotalTokens=11000, got %d", agg.TotalTokens)
	}
	if !floatNear(agg.TotalCostUSD, 0.16, 1e-9) {
		t.Errorf("expected TotalCostUSD=0.16, got %f", agg.TotalCostUSD)
	}
}

func TestFormatToolBreakdownEmpty(t *testing.T) {
	result := formatToolBreakdown(nil)
	if result != "" {
		t.Errorf("expected empty string for nil map, got %q", result)
	}
	result = formatToolBreakdown(map[string]int{})
	if result != "" {
		t.Errorf("expected empty string for empty map, got %q", result)
	}
}

func TestFormatToolBreakdownSorted(t *testing.T) {
	tools := map[string]int{"bash": 50, "write": 10, "read": 30}
	result := formatToolBreakdown(tools)
	// Should be sorted by count descending
	if !strings.HasPrefix(result, "(bash: 50") {
		t.Errorf("expected bash first (highest count), got %q", result)
	}
	if !strings.Contains(result, "read: 30") {
		t.Errorf("expected read in breakdown, got %q", result)
	}
	if !strings.Contains(result, "write: 10") {
		t.Errorf("expected write in breakdown, got %q", result)
	}
}

func TestFormatNumber(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{0, "0"},
		{42, "42"},
		{999, "999"},
		{1000, "1,000"},
		{12345, "12,345"},
		{1234567, "1,234,567"},
	}
	for _, tc := range tests {
		got := formatNumber(tc.input)
		if got != tc.expected {
			t.Errorf("formatNumber(%d) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestPrintRunSummaryShowsTotals(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	now := time.Now()
	result := &pipeline.EngineResult{
		RunID:  "test-run",
		Status: pipeline.OutcomeSuccess,
		Trace: &pipeline.Trace{
			RunID:     "test-run",
			StartTime: now,
			EndTime:   now.Add(5 * time.Minute),
			Entries: []pipeline.TraceEntry{
				{NodeID: "s", HandlerName: "start", Status: "success", Duration: 1 * time.Millisecond},
				{
					NodeID: "impl", HandlerName: "codergen", Status: "success",
					Duration: 4 * time.Minute,
					Stats: &pipeline.SessionStats{
						Turns:          8,
						TotalToolCalls: 42,
						ToolCalls:      map[string]int{"bash": 30, "write": 12},
						FilesCreated:   []string{"new.go"},
						FilesModified:  []string{"main.go"},
						Compactions:    1,
					},
				},
				{NodeID: "end", HandlerName: "exit", Status: "success", Duration: 1 * time.Millisecond},
			},
		},
	}
	printRunSummary(result, nil, "test.dot")

	w.Close()
	os.Stdout = old

	var buf [8192]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	// Verify totals section
	if !strings.Contains(output, "Totals") {
		t.Errorf("expected Totals section in output")
	}
	if !strings.Contains(output, "LLM Turns") {
		t.Errorf("expected LLM Turns in output")
	}
	if !strings.Contains(output, "Tool Calls") {
		t.Errorf("expected Tool Calls in output")
	}
	if !strings.Contains(output, "bash: 30") {
		t.Errorf("expected bash breakdown in output")
	}
	if !strings.Contains(output, "1 created") {
		t.Errorf("expected files created count in output")
	}
	if !strings.Contains(output, "1 modified") {
		t.Errorf("expected files modified count in output")
	}

	// Verify node table has Turns and Tools columns
	if !strings.Contains(output, "Turns") || !strings.Contains(output, "Tools") {
		t.Errorf("expected Turns and Tools columns in node table")
	}

	// Verify logo block characters are present in the output.
	if !strings.Contains(output, "2389.ai") {
		t.Errorf("expected 2389.ai branding in output")
	}

	// Verify Run Complete header
	if !strings.Contains(output, "Run Complete") {
		t.Errorf("expected Run Complete header in output")
	}
}

func TestPrintRunSummaryUsesEngineUsageForTokensAndCost(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	now := time.Now()
	result := &pipeline.EngineResult{
		RunID:  "test-run",
		Status: pipeline.OutcomeSuccess,
		Trace: &pipeline.Trace{
			RunID:     "test-run",
			StartTime: now,
			EndTime:   now.Add(30 * time.Second),
			Entries: []pipeline.TraceEntry{
				{
					NodeID: "impl", HandlerName: "codergen", Status: "success",
					Duration: 20 * time.Second,
					Stats: &pipeline.SessionStats{
						Turns:          1,
						TotalToolCalls: 1,
						ToolCalls:      map[string]int{"bash": 1},
					},
				},
			},
		},
		Usage: &pipeline.UsageSummary{
			TotalInputTokens:  1000,
			TotalOutputTokens: 250,
			TotalTokens:       1250,
			TotalCostUSD:      0.1234,
			ProviderTotals: map[string]pipeline.ProviderUsage{
				"openai": {InputTokens: 1000, OutputTokens: 250, TotalTokens: 1250, CostUSD: 0.1234, SessionCount: 1},
			},
		},
	}

	printRunSummary(result, nil, "test.dot")

	w.Close()
	os.Stdout = old

	var buf [8192]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	if !strings.Contains(output, "Tokens:       1,000 in / 250 out  ($0.12)") {
		t.Fatalf("expected total token/cost line from result.Usage, output:\n%s", output)
	}
	if !strings.Contains(output, "Tokens by Provider") {
		t.Fatalf("expected provider token table from result.Usage, output:\n%s", output)
	}
	if !strings.Contains(output, "Cost: $0.1234") {
		t.Fatalf("expected provider cost line from result.Usage, output:\n%s", output)
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

func TestParseFlagsBackend(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "--backend", "claude-code", "pipeline.dip"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.backend != "claude-code" {
		t.Fatalf("backend = %q, want %q", cfg.backend, "claude-code")
	}
}

func TestParseFlagsBackendNative(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "--backend", "native", "pipeline.dip"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.backend != "native" {
		t.Fatalf("backend = %q, want %q", cfg.backend, "native")
	}
}

func TestParseFlagsBackendInvalid(t *testing.T) {
	_, err := parseFlags([]string{"tracker", "--backend", "foobar", "pipeline.dip"})
	if err == nil {
		t.Fatal("expected error for invalid backend")
	}
}

func TestParseFlagsGatewayURL(t *testing.T) {
	const want = "https://gateway.ai.cloudflare.com/v1/acc/gw"
	cfg, err := parseFlags([]string{"tracker", "--gateway-url", want, "pipeline.dip"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.gatewayURL != want {
		t.Fatalf("gatewayURL = %q, want %q", cfg.gatewayURL, want)
	}
	if cfg.pipelineFile != "pipeline.dip" {
		t.Fatalf("pipelineFile = %q, want %q", cfg.pipelineFile, "pipeline.dip")
	}
}

func TestParseFlagsParamOverrides(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "--param", "foo=bar", "--param", "env=prod", "pipeline.dip"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.params["foo"] != "bar" || cfg.params["env"] != "prod" {
		t.Fatalf("params = %#v, want foo=bar env=prod", cfg.params)
	}
}

func TestParseFlagsParamInvalidFormat(t *testing.T) {
	_, err := parseFlags([]string{"tracker", "--param", "not-a-pair", "pipeline.dip"})
	if err == nil {
		t.Fatal("expected parse error for invalid --param")
	}
	if !strings.Contains(err.Error(), "key=value") {
		t.Fatalf("error = %v, want key=value", err)
	}
}

func TestGatewayURLPropagatesViaEnv(t *testing.T) {
	// executeRun sets TRACKER_GATEWAY_URL before buildLLMClient runs.
	// Verify the env var is live in the same process after executeRun sets it.
	unsetEnvForTest(t, "TRACKER_GATEWAY_URL")

	const gateway = "https://gateway.ai.cloudflare.com/v1/acc/test"
	var envValueAtRunTime string

	_ = executeCommand(runConfig{
		mode:         modeRun,
		pipelineFile: "pipeline.dip",
		workdir:      "/tmp",
		noTUI:        true,
		gatewayURL:   gateway,
	}, commandDeps{
		loadEnv: func(string) error { return nil },
		run: func(pipelineFile, workdir, checkpoint, format, backend string, verbose bool, jsonOut bool) error {
			// By the time run() is called, TRACKER_GATEWAY_URL must be set.
			envValueAtRunTime = os.Getenv("TRACKER_GATEWAY_URL")
			return nil
		},
		runTUI: func(pipelineFile, workdir, checkpoint, format, backend string, verbose bool) error {
			t.Fatal("unexpected TUI path")
			return nil
		},
	})

	if envValueAtRunTime != gateway {
		t.Fatalf("TRACKER_GATEWAY_URL inside run() = %q, want %q", envValueAtRunTime, gateway)
	}
}

func TestResolveProviderBaseURLFromEnvGateway(t *testing.T) {
	// resolveProviderBaseURLFromEnv must return the gateway-suffixed URL when
	// TRACKER_GATEWAY_URL is set and no per-provider override exists.
	unsetEnvForTest(t, "ANTHROPIC_BASE_URL")
	t.Setenv("TRACKER_GATEWAY_URL", "https://gw.example.com/v1/acc/slug")

	got := resolveProviderBaseURLFromEnv("ANTHROPIC_BASE_URL", "/anthropic")
	want := "https://gw.example.com/v1/acc/slug/anthropic"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveProviderBaseURLFromEnvPerProviderWins(t *testing.T) {
	// Per-provider *_BASE_URL must win over TRACKER_GATEWAY_URL.
	t.Setenv("ANTHROPIC_BASE_URL", "https://custom-proxy.example.com")
	t.Setenv("TRACKER_GATEWAY_URL", "https://gw.example.com/v1/acc/slug")

	got := resolveProviderBaseURLFromEnv("ANTHROPIC_BASE_URL", "/anthropic")
	want := "https://custom-proxy.example.com"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveProviderBaseURLFromEnvNoGateway(t *testing.T) {
	// With neither env var set, must return empty string (use provider SDK default).
	unsetEnvForTest(t, "ANTHROPIC_BASE_URL")
	unsetEnvForTest(t, "TRACKER_GATEWAY_URL")

	got := resolveProviderBaseURLFromEnv("ANTHROPIC_BASE_URL", "/anthropic")
	if got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestApplyRunParamOverrides(t *testing.T) {
	g := pipeline.NewGraph("test")
	g.Attrs["params.foo"] = "default"
	activeRunParams = map[string]string{"foo": "bar"}
	t.Cleanup(func() {
		activeRunParams = nil
		activeEffectiveRunParams = nil
	})

	if err := applyRunParamOverrides(g); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := g.Attrs["params.foo"]; got != "bar" {
		t.Fatalf("params.foo = %q, want bar", got)
	}
	if got := activeEffectiveRunParams["foo"]; got != "bar" {
		t.Fatalf("activeEffectiveRunParams.foo = %q, want bar", got)
	}
}

func TestApplyRunParamOverridesUnknownParam(t *testing.T) {
	g := pipeline.NewGraph("test")
	activeRunParams = map[string]string{"missing": "bar"}
	t.Cleanup(func() {
		activeRunParams = nil
		activeEffectiveRunParams = nil
	})

	err := applyRunParamOverrides(g)
	if err == nil {
		t.Fatal("expected error for unknown param")
	}
	if !strings.Contains(err.Error(), "unknown param") {
		t.Fatalf("error = %v, want unknown param", err)
	}
}

func TestExecuteCommandRunPassesBackend(t *testing.T) {
	var gotBackend string
	err := executeCommand(runConfig{
		mode:         modeRun,
		pipelineFile: "pipeline.dip",
		workdir:      "/tmp",
		noTUI:        true,
		backend:      "claude-code",
	}, commandDeps{
		loadEnv: func(string) error { return nil },
		run: func(pipelineFile, workdir, checkpoint, format, backend string, verbose bool, jsonOut bool) error {
			gotBackend = backend
			return nil
		},
		runTUI: func(pipelineFile, workdir, checkpoint, format, backend string, verbose bool) error {
			t.Fatal("unexpected TUI path")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("executeCommand error: %v", err)
	}
	if gotBackend != "claude-code" {
		t.Fatalf("backend = %q, want %q", gotBackend, "claude-code")
	}
}

// TestParseFlagsExportBundle verifies that --export-bundle is parsed into
// runConfig.exportBundle without modifying other fields.
func TestParseFlagsExportBundle(t *testing.T) {
	const bundlePath = "/tmp/myrun.bundle"
	cfg, err := parseFlags([]string{"tracker", "--export-bundle", bundlePath, "pipeline.dip"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.exportBundle != bundlePath {
		t.Fatalf("exportBundle = %q, want %q", cfg.exportBundle, bundlePath)
	}
	if cfg.pipelineFile != "pipeline.dip" {
		t.Fatalf("pipelineFile = %q, want %q", cfg.pipelineFile, "pipeline.dip")
	}
}

// TestExportBundleFieldPassedToActiveGlobal verifies that executeRun propagates
// cfg.exportBundle into the activeExportBundle global, which maybeExportBundle reads.
func TestExportBundleFieldPassedToActiveGlobal(t *testing.T) {
	const bundlePath = "/tmp/testroundtrip.bundle"

	t.Cleanup(func() { activeExportBundle = "" })

	var captured string
	_ = executeCommand(runConfig{
		mode:         modeRun,
		pipelineFile: "pipeline.dip",
		workdir:      "/tmp",
		noTUI:        true,
		exportBundle: bundlePath,
	}, commandDeps{
		loadEnv: func(string) error { return nil },
		run: func(pipelineFile, workdir, checkpoint, format, backend string, verbose bool, jsonOut bool) error {
			// Capture the global set by executeRun.
			captured = activeExportBundle
			return nil
		},
		runTUI: func(pipelineFile, workdir, checkpoint, format, backend string, verbose bool) error {
			t.Fatal("unexpected TUI path")
			return nil
		},
	})
	if captured != bundlePath {
		t.Fatalf("activeExportBundle inside run() = %q, want %q", captured, bundlePath)
	}
}

// TestParseFlagsArtifactDir verifies that --artifact-dir is parsed into
// runConfig.artifactDir without modifying other fields.
func TestParseFlagsArtifactDir(t *testing.T) {
	const dir = "/tmp/custom-artifacts"
	cfg, err := parseFlags([]string{"tracker", "--artifact-dir", dir, "pipeline.dip"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.artifactDir != dir {
		t.Fatalf("artifactDir = %q, want %q", cfg.artifactDir, dir)
	}
	if cfg.pipelineFile != "pipeline.dip" {
		t.Fatalf("pipelineFile = %q, want %q", cfg.pipelineFile, "pipeline.dip")
	}
}

// TestArtifactDirFieldPassedToActiveGlobal verifies that executeRun propagates
// cfg.artifactDir into the activeArtifactDir global, which run/runTUI reads.
func TestArtifactDirFieldPassedToActiveGlobal(t *testing.T) {
	const dir = "/tmp/custom-artifacts"

	t.Cleanup(func() { activeArtifactDir = "" })

	var captured string
	_ = executeCommand(runConfig{
		mode:         modeRun,
		pipelineFile: "pipeline.dip",
		workdir:      "/tmp",
		noTUI:        true,
		artifactDir:  dir,
	}, commandDeps{
		loadEnv: func(string) error { return nil },
		run: func(pipelineFile, workdir, checkpoint, format, backend string, verbose bool, jsonOut bool) error {
			captured = activeArtifactDir
			return nil
		},
		runTUI: func(pipelineFile, workdir, checkpoint, format, backend string, verbose bool) error {
			t.Fatal("unexpected TUI path")
			return nil
		},
	})
	if captured != dir {
		t.Fatalf("activeArtifactDir inside run() = %q, want %q", captured, dir)
	}
}
