package main

import (
	"strings"
	"testing"
	"time"
)

// TestParseFlagsWebhook_AllFlagsSet verifies that all webhook flags are parsed correctly.
func TestParseFlagsWebhook_AllFlagsSet(t *testing.T) {
	cfg, err := parseFlags([]string{
		"tracker",
		"--webhook-url", "http://x",
		"--gate-callback-addr", ":9999",
		"--gate-timeout", "5m",
		"--gate-timeout-action", "success",
		"--webhook-auth", "Bearer x",
		"pipeline.dip",
	})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.webhookURL != "http://x" {
		t.Errorf("webhookURL = %q, want %q", cfg.webhookURL, "http://x")
	}
	if cfg.gateCallbackAddr != ":9999" {
		t.Errorf("gateCallbackAddr = %q, want %q", cfg.gateCallbackAddr, ":9999")
	}
	if cfg.gateTimeout != 5*time.Minute {
		t.Errorf("gateTimeout = %v, want 5m", cfg.gateTimeout)
	}
	if cfg.gateTimeoutAction != "success" {
		t.Errorf("gateTimeoutAction = %q, want %q", cfg.gateTimeoutAction, "success")
	}
	if cfg.webhookAuthHeader != "Bearer x" {
		t.Errorf("webhookAuthHeader = %q, want %q", cfg.webhookAuthHeader, "Bearer x")
	}
}

// TestParseFlagsWebhook_DefaultCallbackAddr verifies that omitted webhook flags use correct defaults.
func TestParseFlagsWebhook_DefaultCallbackAddr(t *testing.T) {
	cfg, err := parseFlags([]string{
		"tracker",
		"--webhook-url", "http://example.com/gate",
		"pipeline.dip",
	})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.gateCallbackAddr != ":8789" {
		t.Errorf("gateCallbackAddr = %q, want default %q", cfg.gateCallbackAddr, ":8789")
	}
	if cfg.gateTimeout != 10*time.Minute {
		t.Errorf("gateTimeout = %v, want default 10m", cfg.gateTimeout)
	}
}

// TestParseFlagsWebhook_MutualExclusionAutopilot verifies that --webhook-url and --autopilot cannot be combined.
func TestParseFlagsWebhook_MutualExclusionAutopilot(t *testing.T) {
	_, err := parseFlags([]string{
		"tracker",
		"--webhook-url", "http://x",
		"--autopilot", "mid",
		"pipeline.dip",
	})
	if err == nil {
		t.Fatal("expected error when combining --webhook-url and --autopilot, got nil")
	}
	if !strings.Contains(err.Error(), "--webhook-url") || !strings.Contains(err.Error(), "--autopilot") {
		t.Errorf("error message should mention both flags, got: %v", err)
	}
}

// TestParseFlagsWebhook_MutualExclusionAutoApprove verifies that --webhook-url and --auto-approve cannot be combined.
func TestParseFlagsWebhook_MutualExclusionAutoApprove(t *testing.T) {
	_, err := parseFlags([]string{
		"tracker",
		"--webhook-url", "http://x",
		"--auto-approve",
		"pipeline.dip",
	})
	if err == nil {
		t.Fatal("expected error when combining --webhook-url and --auto-approve, got nil")
	}
	if !strings.Contains(err.Error(), "--webhook-url") || !strings.Contains(err.Error(), "--auto-approve") {
		t.Errorf("error message should mention both flags, got: %v", err)
	}
}

// TestParseFlagsWebhook_InvalidTimeoutAction verifies that unsupported timeout actions are rejected.
func TestParseFlagsWebhook_InvalidTimeoutAction(t *testing.T) {
	_, err := parseFlags([]string{
		"tracker",
		"--webhook-url", "http://x",
		"--gate-timeout-action", "banana",
		"pipeline.dip",
	})
	if err == nil {
		t.Fatal("expected error for invalid --gate-timeout-action, got nil")
	}
	if !strings.Contains(err.Error(), "banana") {
		t.Errorf("error should mention the invalid value, got: %v", err)
	}
	if !strings.Contains(err.Error(), "fail") || !strings.Contains(err.Error(), "success") {
		t.Errorf("error should mention valid values, got: %v", err)
	}
}

// TestBuildWebhookGateConfig_EmptyReturnsNil verifies that an empty webhookURL produces nil.
func TestBuildWebhookGateConfig_EmptyReturnsNil(t *testing.T) {
	cfg := runConfig{webhookURL: ""}
	result := buildWebhookGateConfig(cfg)
	if result != nil {
		t.Errorf("expected nil for empty webhookURL, got %+v", result)
	}
}

// TestBuildWebhookGateConfig_PopulatesAllFields verifies that a non-empty URL produces a fully-populated config.
func TestBuildWebhookGateConfig_PopulatesAllFields(t *testing.T) {
	cfg := runConfig{
		webhookURL:        "https://example.com/gate",
		gateCallbackAddr:  ":9000",
		gateTimeout:       15 * time.Minute,
		gateTimeoutAction: "success",
		webhookAuthHeader: "Bearer token123",
	}
	result := buildWebhookGateConfig(cfg)
	if result == nil {
		t.Fatal("expected non-nil webhookGateCfg")
	}
	if result.webhookURL != cfg.webhookURL {
		t.Errorf("webhookURL = %q, want %q", result.webhookURL, cfg.webhookURL)
	}
	if result.gateCallbackAddr != cfg.gateCallbackAddr {
		t.Errorf("gateCallbackAddr = %q, want %q", result.gateCallbackAddr, cfg.gateCallbackAddr)
	}
	if result.gateTimeout != cfg.gateTimeout {
		t.Errorf("gateTimeout = %v, want %v", result.gateTimeout, cfg.gateTimeout)
	}
	if result.gateTimeoutAction != cfg.gateTimeoutAction {
		t.Errorf("gateTimeoutAction = %q, want %q", result.gateTimeoutAction, cfg.gateTimeoutAction)
	}
	if result.webhookAuthHeader != cfg.webhookAuthHeader {
		t.Errorf("webhookAuthHeader = %q, want %q", result.webhookAuthHeader, cfg.webhookAuthHeader)
	}
}
