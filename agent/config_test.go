// ABOUTME: Tests for SessionConfig defaults and validation.
// ABOUTME: Verifies sensible defaults are applied and invalid configs are rejected.
package agent

import (
	"testing"
	"time"
)

func TestConfigDefaults(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.MaxTurns != 50 {
		t.Errorf("expected MaxTurns 50, got %d", cfg.MaxTurns)
	}
	if cfg.CommandTimeout != 10*time.Second {
		t.Errorf("expected CommandTimeout 10s, got %v", cfg.CommandTimeout)
	}
	if cfg.MaxCommandTimeout != 10*time.Minute {
		t.Errorf("expected MaxCommandTimeout 10m, got %v", cfg.MaxCommandTimeout)
	}
	if cfg.LoopDetectionThreshold != 10 {
		t.Errorf("expected LoopDetectionThreshold 10, got %d", cfg.LoopDetectionThreshold)
	}
	if cfg.WorkingDir != "." {
		t.Errorf("expected WorkingDir '.', got %q", cfg.WorkingDir)
	}
	if cfg.Model != DefaultModel {
		t.Errorf("expected Model %q, got %q", DefaultModel, cfg.Model)
	}
	if cfg.Provider != DefaultProvider {
		t.Errorf("expected Provider %q, got %q", DefaultProvider, cfg.Provider)
	}
}

func TestConfigValidation(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Errorf("default config should be valid: %v", err)
	}

	bad := SessionConfig{MaxTurns: 0}
	if err := bad.Validate(); err == nil {
		t.Error("expected error for MaxTurns=0")
	}

	// Zero timeout should fail.
	zeroTimeout := DefaultConfig()
	zeroTimeout.CommandTimeout = 0
	if err := zeroTimeout.Validate(); err == nil {
		t.Error("expected error for CommandTimeout=0")
	}

	// MaxCommandTimeout < CommandTimeout should fail.
	invertedTimeouts := DefaultConfig()
	invertedTimeouts.CommandTimeout = 10 * time.Minute
	invertedTimeouts.MaxCommandTimeout = 5 * time.Second
	if err := invertedTimeouts.Validate(); err == nil {
		t.Error("expected error when MaxCommandTimeout < CommandTimeout")
	}
}

func TestDefaultConfig_CacheToolResultsIsFalse(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.CacheToolResults {
		t.Fatal("CacheToolResults should default to false")
	}
}

func TestValidate_CacheToolResultsAcceptsBothValues(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CacheToolResults = true
	if err := cfg.Validate(); err != nil {
		t.Errorf("CacheToolResults=true should be valid: %v", err)
	}
	cfg.CacheToolResults = false
	if err := cfg.Validate(); err != nil {
		t.Errorf("CacheToolResults=false should be valid: %v", err)
	}
}

func TestValidate_ResponseFormat(t *testing.T) {
	cfg := DefaultConfig()

	// Valid: empty (default)
	if err := cfg.Validate(); err != nil {
		t.Errorf("empty ResponseFormat should be valid: %v", err)
	}

	// Valid: json_object
	cfg.ResponseFormat = "json_object"
	if err := cfg.Validate(); err != nil {
		t.Errorf("json_object should be valid: %v", err)
	}

	// Valid: json_schema with valid schema
	cfg.ResponseFormat = "json_schema"
	cfg.ResponseSchema = `{"type": "object"}`
	if err := cfg.Validate(); err != nil {
		t.Errorf("json_schema with valid schema should be valid: %v", err)
	}

	// Invalid: unknown format
	cfg.ResponseFormat = "xml"
	cfg.ResponseSchema = ""
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for ResponseFormat=xml")
	}

	// Invalid: json_schema without schema
	cfg.ResponseFormat = "json_schema"
	cfg.ResponseSchema = ""
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for json_schema without ResponseSchema")
	}

	// Invalid: json_schema with invalid JSON
	cfg.ResponseSchema = "not json"
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for invalid ResponseSchema JSON")
	}
}
