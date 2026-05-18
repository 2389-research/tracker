// ABOUTME: Tests for --git and --allow-init flag parsing (v0.29.0).
package main

import (
	"strings"
	"testing"
)

func TestParseFlags_GitPolicyValid(t *testing.T) {
	cases := []string{"off", "warn", "require", "init", "auto"}
	for _, v := range cases {
		t.Run(v, func(t *testing.T) {
			args := []string{"tracker", "run.dip", "--git=" + v}
			cfg, err := parseFlags(args)
			if err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			want := v
			if v == "auto" {
				want = "" // resolves to GitPreflightAuto
			}
			if cfg.git != want {
				t.Errorf("git: want %q, got %q", want, cfg.git)
			}
		})
	}
}

func TestParseFlags_GitPolicyInvalid(t *testing.T) {
	args := []string{"tracker", "run.dip", "--git=bogus"}
	_, err := parseFlags(args)
	if err == nil {
		t.Fatalf("expected error on invalid --git value")
	}
	if !strings.Contains(err.Error(), "auto") || !strings.Contains(err.Error(), "off") {
		t.Errorf("error must list valid values, got %v", err)
	}
}

func TestParseFlags_AllowInit(t *testing.T) {
	args := []string{"tracker", "run.dip", "--git=init", "--allow-init"}
	cfg, err := parseFlags(args)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if cfg.git != "init" {
		t.Errorf("git: want %q, got %q", "init", cfg.git)
	}
	if !cfg.allowInit {
		t.Errorf("allowInit: want true")
	}
}

func TestParseFlags_DoctorGitFlag(t *testing.T) {
	args := []string{"tracker", "doctor", "--git=warn"}
	cfg, err := parseFlags(args)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if cfg.git != "warn" {
		t.Errorf("doctor git: want %q, got %q", "warn", cfg.git)
	}
}
