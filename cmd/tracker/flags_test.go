// ABOUTME: Tests for --git and --allow-init flag parsing (v0.29.0).
package main

import (
	"errors"
	"flag"
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

func TestParseFlags_VerifyTestsRace(t *testing.T) {
	// --race flag and the dir positional parse in either order (#489).
	for _, args := range [][]string{
		{"tracker", "verify-tests", "src", "--race"},
		{"tracker", "verify-tests", "--race", "src"},
	} {
		cfg, err := parseFlags(args)
		if err != nil {
			t.Fatalf("%v: unexpected error: %v", args, err)
		}
		if !cfg.verifyRace {
			t.Errorf("%v: verifyRace should be true", args)
		}
		if cfg.pipelineFile != "src" {
			t.Errorf("%v: dir: want %q, got %q", args, "src", cfg.pipelineFile)
		}
	}

	// Without --race the flag defaults off.
	cfg, err := parseFlags([]string{"tracker", "verify-tests", "."})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.verifyRace {
		t.Error("verifyRace should default to false")
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

// TestParseFlags_HelpRouting verifies the two-tier help (#463): the plain help
// forms return flag.ErrHelp (common flags) while --help-all returns errHelpAll
// (the full grouped reference).
func TestParseFlags_HelpRouting(t *testing.T) {
	cases := []struct {
		arg  string
		want error
	}{
		{"--help", flag.ErrHelp},
		{"-h", flag.ErrHelp},
		{"help", flag.ErrHelp},
		{"--help-all", errHelpAll},
		{"help-all", errHelpAll},
	}
	for _, c := range cases {
		t.Run(c.arg, func(t *testing.T) {
			_, err := parseFlags([]string{"tracker", c.arg})
			if !errors.Is(err, c.want) {
				t.Fatalf("arg %q: want %v, got %v", c.arg, c.want, err)
			}
		})
	}
}
