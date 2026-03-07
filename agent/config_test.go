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
