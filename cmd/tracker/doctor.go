// ABOUTME: Preflight health check — verifies API keys, dippin binary, and workdir.
// ABOUTME: Surfaces actionable guidance for common setup issues.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// formatLLMClientError wraps LLM client creation errors with actionable hints.
func formatLLMClientError(err error) error {
	if strings.Contains(err.Error(), "no providers configured") {
		return fmt.Errorf(`no LLM providers configured

  Set at least one API key:
    export ANTHROPIC_API_KEY=sk-ant-...
    export OPENAI_API_KEY=sk-...
    export GEMINI_API_KEY=...

  Or run: tracker setup`)
	}
	return fmt.Errorf("create LLM client: %w", err)
}

// runDoctor performs a preflight health check and prints results.
func runDoctor(workdir string) error {
	fmt.Println()
	fmt.Println(bannerStyle.Render("tracker doctor"))
	fmt.Println()

	pass := true

	// Check LLM providers.
	pass = checkProviders() && pass
	fmt.Println()

	// Check dippin binary.
	pass = checkDippin() && pass
	fmt.Println()

	// Check workdir.
	pass = checkWorkdir(workdir) && pass
	fmt.Println()

	// Summary.
	if pass {
		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(colorNeon).Render("  All checks passed"))
	} else {
		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(colorHot).Render("  Some checks failed — see above"))
	}
	fmt.Println()

	if !pass {
		return fmt.Errorf("health check failed")
	}
	return nil
}

func checkProviders() bool {
	fmt.Println("  LLM Providers")
	allPass := false
	providers := []struct {
		name    string
		envVars []string
	}{
		{"Anthropic", []string{"ANTHROPIC_API_KEY"}},
		{"OpenAI", []string{"OPENAI_API_KEY"}},
		{"Gemini", []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}},
	}
	for _, p := range providers {
		found := false
		for _, env := range p.envVars {
			if v := os.Getenv(env); v != "" {
				masked := maskKey(v)
				printCheck(true, fmt.Sprintf("%-10s %s=%s", p.name, env, masked))
				found = true
				allPass = true
				break
			}
		}
		if !found {
			printCheck(false, fmt.Sprintf("%-10s %s not set", p.name, p.envVars[0]))
		}
	}
	if !allPass {
		printHint("Run `tracker setup` to configure providers")
	}
	return allPass
}

func checkDippin() bool {
	fmt.Println("  Dippin Language")
	path, err := exec.LookPath("dippin")
	if err != nil {
		printCheck(false, "dippin binary not found in PATH")
		printHint("Install from https://github.com/2389-research/dippin-lang")
		return false
	}
	// Get version.
	out, err := exec.Command(path, "version").CombinedOutput()
	if err != nil {
		printCheck(true, fmt.Sprintf("dippin found at %s (version unknown)", path))
		return true
	}
	ver := strings.TrimSpace(string(out))
	printCheck(true, fmt.Sprintf("dippin %s", ver))
	return true
}

func checkWorkdir(workdir string) bool {
	fmt.Println("  Working Directory")
	if workdir == "" {
		var err error
		workdir, err = os.Getwd()
		if err != nil {
			printCheck(false, "cannot determine working directory")
			return false
		}
	}

	info, err := os.Stat(workdir)
	if err != nil {
		printCheck(false, fmt.Sprintf("%s does not exist", workdir))
		return false
	}
	if !info.IsDir() {
		printCheck(false, fmt.Sprintf("%s is not a directory", workdir))
		return false
	}

	// Check if .tracker dir exists or can be created.
	trackerDir := workdir + "/.tracker"
	if _, err := os.Stat(trackerDir); err == nil {
		printCheck(true, fmt.Sprintf("%s (artifacts dir exists)", workdir))
	} else {
		printCheck(true, fmt.Sprintf("%s (artifacts dir will be created on first run)", workdir))
	}
	return true
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

func printCheck(ok bool, msg string) {
	if ok {
		fmt.Printf("    %s %s\n", lipgloss.NewStyle().Foreground(colorNeon).Render("✓"), msg)
	} else {
		fmt.Printf("    %s %s\n", lipgloss.NewStyle().Foreground(colorHot).Render("✗"), msg)
	}
}

func printHint(msg string) {
	fmt.Printf("    %s\n", mutedStyle.Render("→ "+msg))
}
