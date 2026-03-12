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

func TestConfigEnvMergeProviderBaseURLUpdates(t *testing.T) {
	existing := map[string]string{
		"OPENAI_API_KEY": "my-key",
	}
	updates := map[string]string{
		"OPENAI_BASE_URL":    "https://custom.openai.example.com",
		"ANTHROPIC_BASE_URL": "https://custom.anthropic.example.com",
		"GEMINI_BASE_URL":    "https://custom.gemini.example.com",
	}

	merged := mergeProviderEnv(existing, updates)

	if merged["OPENAI_BASE_URL"] != "https://custom.openai.example.com" {
		t.Fatalf("OPENAI_BASE_URL = %q, want %q", merged["OPENAI_BASE_URL"], "https://custom.openai.example.com")
	}
	if merged["ANTHROPIC_BASE_URL"] != "https://custom.anthropic.example.com" {
		t.Fatalf("ANTHROPIC_BASE_URL = %q, want %q", merged["ANTHROPIC_BASE_URL"], "https://custom.anthropic.example.com")
	}
	if merged["GEMINI_BASE_URL"] != "https://custom.gemini.example.com" {
		t.Fatalf("GEMINI_BASE_URL = %q, want %q", merged["GEMINI_BASE_URL"], "https://custom.gemini.example.com")
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

	// Verify round-trip: read back should produce identical values.
	readBack, err := readEnvFile(path)
	if err != nil {
		t.Fatalf("readEnvFile returned error: %v", err)
	}
	for key, want := range values {
		if readBack[key] != want {
			t.Fatalf("round-trip %s = %q, want %q", key, readBack[key], want)
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat written env file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions = %o, want %o", info.Mode().Perm(), 0o600)
	}
}

func TestConfigEnvWriteEnvFileRoundTripsSpecialCharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	values := map[string]string{
		"KEY_WITH_HASH":  "sk-abc123#456",
		"KEY_WITH_SPACE": "has spaces in it",
		"KEY_WITH_QUOTE": `has"quote`,
		"URL_WITH_HASH":  "https://example.com/api#fragment",
	}

	if err := writeEnvFile(path, values); err != nil {
		t.Fatalf("writeEnvFile returned error: %v", err)
	}

	readBack, err := readEnvFile(path)
	if err != nil {
		t.Fatalf("readEnvFile returned error: %v", err)
	}

	for key, want := range values {
		if readBack[key] != want {
			t.Fatalf("round-trip %s = %q, want %q", key, readBack[key], want)
		}
	}
}
