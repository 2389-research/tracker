// ABOUTME: Tests for the conformance CLI binary subcommand dispatch.
// ABOUTME: Covers unknown subcommands, no args, list-models, and client-from-env behaviors.
package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestNoSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"conformance"}, &stdout, &stderr)

	if code == 0 {
		t.Fatal("expected non-zero exit code when no subcommand given")
	}

	if stderr.Len() == 0 {
		t.Fatal("expected usage message on stderr")
	}
}

func TestUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"conformance", "bogus-command"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("expected JSON on stdout, got: %s (err: %v)", stdout.String(), err)
	}

	errMsg, ok := result["error"].(string)
	if !ok {
		t.Fatal("expected 'error' key in JSON response")
	}
	if errMsg != "not implemented: bogus-command" {
		t.Fatalf("unexpected error message: %s", errMsg)
	}
}

func TestListModels(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"conformance", "list-models"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr.String())
	}

	var models []map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &models); err != nil {
		t.Fatalf("expected JSON array on stdout, got: %s (err: %v)", stdout.String(), err)
	}

	if len(models) == 0 {
		t.Fatal("expected at least one model in list")
	}

	// Verify each model has an "id" and "provider" field.
	for i, m := range models {
		if _, ok := m["id"]; !ok {
			t.Fatalf("model %d missing 'id' field", i)
		}
		if _, ok := m["provider"]; !ok {
			t.Fatalf("model %d missing 'provider' field", i)
		}
	}

	// Check that known model IDs are present.
	ids := make(map[string]bool)
	for _, m := range models {
		ids[m["id"].(string)] = true
	}

	expectedIDs := []string{"claude-opus-4-6", "gpt-5.2", "gemini-3-pro-preview"}
	for _, expected := range expectedIDs {
		if !ids[expected] {
			t.Errorf("expected model ID %q in list", expected)
		}
	}
}

func TestClientFromEnvWithKeys(t *testing.T) {
	// Set a mock API key so the client-from-env subcommand can find it.
	t.Setenv("ANTHROPIC_API_KEY", "test-key-anthropic")
	t.Setenv("OPENAI_API_KEY", "test-key-openai")
	// Clear google key to test partial provider detection.
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")

	var stdout, stderr bytes.Buffer
	code := run([]string{"conformance", "client-from-env"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr.String())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("expected JSON on stdout, got: %s (err: %v)", stdout.String(), err)
	}

	if result["status"] != "ok" {
		t.Fatalf("expected status 'ok', got %v", result["status"])
	}

	providers, ok := result["providers"].([]interface{})
	if !ok {
		t.Fatal("expected 'providers' to be an array")
	}

	providerSet := make(map[string]bool)
	for _, p := range providers {
		providerSet[p.(string)] = true
	}

	if !providerSet["anthropic"] {
		t.Error("expected 'anthropic' in providers list")
	}
	if !providerSet["openai"] {
		t.Error("expected 'openai' in providers list")
	}
	if providerSet["google"] {
		t.Error("did not expect 'google' in providers list (no key set)")
	}
}

func TestClientFromEnvNoKeys(t *testing.T) {
	// Clear all API keys.
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")

	var stdout, stderr bytes.Buffer
	code := run([]string{"conformance", "client-from-env"}, &stdout, &stderr)

	if code == 0 {
		t.Fatal("expected non-zero exit code when no keys configured")
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("expected JSON on stdout, got: %s (err: %v)", stdout.String(), err)
	}

	if _, ok := result["error"]; !ok {
		t.Fatal("expected 'error' key in JSON response")
	}
}

func TestUnimplementedSubcommands(t *testing.T) {
	unimplemented := []string{
		"complete", "stream", "tool-call", "generate-object",
		"session-create", "process-input", "tool-dispatch", "steering", "events",
		"parse", "validate", "run", "list-handlers",
	}

	for _, cmd := range unimplemented {
		t.Run(cmd, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run([]string{"conformance", cmd}, &stdout, &stderr)

			if code != 1 {
				t.Fatalf("expected exit code 1 for unimplemented %q, got %d", cmd, code)
			}

			var result map[string]interface{}
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatalf("expected JSON for %q, got: %s (err: %v)", cmd, stdout.String(), err)
			}

			expected := "not implemented: " + cmd
			if result["error"] != expected {
				t.Fatalf("expected error %q, got %q", expected, result["error"])
			}
		})
	}
}
