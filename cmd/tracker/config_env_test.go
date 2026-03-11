package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigEnvResolvePathUsesXDGConfigHome(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	path, err := resolveConfigEnvPath()
	if err != nil {
		t.Fatalf("resolveConfigEnvPath returned error: %v", err)
	}

	want := filepath.Join(configHome, "tracker", ".env")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
}

func TestConfigEnvReadEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("OPENAI_API_KEY=openai-key\nEXTRA_FLAG=yes\n"), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	values, err := readEnvFile(path)
	if err != nil {
		t.Fatalf("readEnvFile returned error: %v", err)
	}

	if values["OPENAI_API_KEY"] != "openai-key" {
		t.Fatalf("OPENAI_API_KEY = %q, want %q", values["OPENAI_API_KEY"], "openai-key")
	}
	if values["EXTRA_FLAG"] != "yes" {
		t.Fatalf("EXTRA_FLAG = %q, want %q", values["EXTRA_FLAG"], "yes")
	}
}

func TestConfigEnvMergeProviderUpdates(t *testing.T) {
	existing := map[string]string{
		"OPENAI_API_KEY": "old-openai",
		"GEMINI_API_KEY": "old-gemini",
		"EXTRA_FLAG":     "keep-me",
	}
	updates := map[string]string{
		"OPENAI_API_KEY":     "new-openai",
		"ANTHROPIC_API_KEY":  "new-anthropic",
		"GEMINI_API_KEY":     "",
		"UNRELATED_ENV_FLAG": "ignore-me",
	}

	merged := mergeProviderEnv(existing, updates)

	if merged["OPENAI_API_KEY"] != "new-openai" {
		t.Fatalf("OPENAI_API_KEY = %q, want %q", merged["OPENAI_API_KEY"], "new-openai")
	}
	if merged["ANTHROPIC_API_KEY"] != "new-anthropic" {
		t.Fatalf("ANTHROPIC_API_KEY = %q, want %q", merged["ANTHROPIC_API_KEY"], "new-anthropic")
	}
	if merged["GEMINI_API_KEY"] != "old-gemini" {
		t.Fatalf("GEMINI_API_KEY = %q, want %q", merged["GEMINI_API_KEY"], "old-gemini")
	}
	if merged["EXTRA_FLAG"] != "keep-me" {
		t.Fatalf("EXTRA_FLAG = %q, want %q", merged["EXTRA_FLAG"], "keep-me")
	}
	if _, exists := merged["UNRELATED_ENV_FLAG"]; exists {
		t.Fatal("did not expect unrelated update key to be added")
	}
}

func TestConfigEnvWriteEnvFileCreatesDirectoriesAndWritesValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "tracker", ".env")
	values := map[string]string{
		"OPENAI_API_KEY":    "openai-key",
		"ANTHROPIC_API_KEY": "anthropic-key",
		"EXTRA_FLAG":        "keep-me",
	}

	if err := writeEnvFile(path, values); err != nil {
		t.Fatalf("writeEnvFile returned error: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written env file: %v", err)
	}

	got := string(content)
	if !containsLine(got, "OPENAI_API_KEY=openai-key") {
		t.Fatalf("written content missing OPENAI_API_KEY: %q", got)
	}
	if !containsLine(got, "ANTHROPIC_API_KEY=anthropic-key") {
		t.Fatalf("written content missing ANTHROPIC_API_KEY: %q", got)
	}
	if !containsLine(got, "EXTRA_FLAG=keep-me") {
		t.Fatalf("written content missing EXTRA_FLAG: %q", got)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat written env file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions = %o, want %o", info.Mode().Perm(), 0o600)
	}
}

func containsLine(content, want string) bool {
	for _, line := range splitLines(content) {
		if line == want {
			return true
		}
	}
	return false
}

func splitLines(content string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			lines = append(lines, content[start:i])
			start = i + 1
		}
	}
	if start < len(content) {
		lines = append(lines, content[start:])
	}
	return lines
}
