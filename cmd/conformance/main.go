// ABOUTME: CLI entry point for the attractorbench conformance binary.
// ABOUTME: Dispatches subcommands (client-from-env, list-models, etc.) reading JSON from stdin, writing JSON to stdout.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/2389-research/mammoth-lite/llm"
	"github.com/2389-research/mammoth-lite/llm/anthropic"
	"github.com/2389-research/mammoth-lite/llm/google"
	"github.com/2389-research/mammoth-lite/llm/openai"
)

func main() {
	code := run(os.Args, os.Stdout, os.Stderr)
	os.Exit(code)
}

// run executes the conformance CLI, dispatching on args[1] as the subcommand.
// Output goes to stdout; errors and usage go to stderr. Returns the exit code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, "Usage: conformance <subcommand>")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Subcommands:")
		fmt.Fprintln(stderr, "  client-from-env   Check provider configuration from env vars")
		fmt.Fprintln(stderr, "  list-models       List all known models")
		fmt.Fprintln(stderr, "  complete           Send a completion request")
		fmt.Fprintln(stderr, "  stream             Send a streaming request")
		fmt.Fprintln(stderr, "  tool-call          Execute a tool call")
		fmt.Fprintln(stderr, "  generate-object    Generate a structured object")
		fmt.Fprintln(stderr, "  session-create     Create a conversation session")
		fmt.Fprintln(stderr, "  process-input      Process user input in a session")
		fmt.Fprintln(stderr, "  tool-dispatch      Dispatch tool execution")
		fmt.Fprintln(stderr, "  steering           Apply mid-session steering")
		fmt.Fprintln(stderr, "  events             List pipeline events")
		fmt.Fprintln(stderr, "  parse              Parse a pipeline DOT file")
		fmt.Fprintln(stderr, "  validate           Validate a pipeline graph")
		fmt.Fprintln(stderr, "  run                Run a pipeline")
		fmt.Fprintln(stderr, "  list-handlers      List registered handlers")
		return 1
	}

	subcmd := args[1]

	switch subcmd {
	case "client-from-env":
		return handleClientFromEnv(stdout, stderr)
	case "list-models":
		return handleListModels(stdout, stderr)
	default:
		return handleNotImplemented(subcmd, stdout)
	}
}

// handleNotImplemented writes a standard "not implemented" JSON error for unknown or
// not-yet-implemented subcommands.
func handleNotImplemented(subcmd string, stdout io.Writer) int {
	writeJSON(stdout, map[string]string{
		"error": "not implemented: " + subcmd,
	})
	return 1
}

// handleClientFromEnv checks which LLM providers are configured via environment
// variables and reports them.
func handleClientFromEnv(stdout, stderr io.Writer) int {
	// Detect which providers have API keys set, using the same env var mapping
	// as llm.NewClientFromEnv.
	type providerCheck struct {
		name    string
		envVars []string
	}

	checks := []providerCheck{
		{"anthropic", []string{"ANTHROPIC_API_KEY"}},
		{"openai", []string{"OPENAI_API_KEY"}},
		{"google", []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}},
	}

	var availableProviders []string
	for _, pc := range checks {
		for _, envVar := range pc.envVars {
			if v := os.Getenv(envVar); v != "" {
				availableProviders = append(availableProviders, pc.name)
				break
			}
		}
	}

	if len(availableProviders) == 0 {
		writeJSON(stdout, map[string]string{
			"error": "no API keys configured",
		})
		fmt.Fprintln(stderr, "error: no API keys configured; set ANTHROPIC_API_KEY, OPENAI_API_KEY, or GEMINI_API_KEY")
		return 1
	}

	// Validate that the client can actually be constructed with the detected keys.
	constructors := buildConstructors()
	client, err := llm.NewClientFromEnv(constructors)
	if err != nil {
		writeJSON(stdout, map[string]string{
			"error": fmt.Sprintf("client creation failed: %v", err),
		})
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	client.Close()

	writeJSON(stdout, map[string]interface{}{
		"status":    "ok",
		"providers": availableProviders,
	})
	return 0
}

// handleListModels writes the full model catalog as a JSON array to stdout.
func handleListModels(stdout, stderr io.Writer) int {
	models := llm.ListModels("")
	if err := writeJSON(stdout, models); err != nil {
		fmt.Fprintf(stderr, "error: failed to encode models: %v\n", err)
		return 1
	}
	return 0
}

// buildConstructors returns the provider constructor map matching the pattern
// used in cmd/mammoth/main.go.
func buildConstructors() map[string]func(string) (llm.ProviderAdapter, error) {
	return map[string]func(string) (llm.ProviderAdapter, error){
		"anthropic": func(key string) (llm.ProviderAdapter, error) {
			var opts []anthropic.Option
			if base := os.Getenv("ANTHROPIC_BASE_URL"); base != "" {
				opts = append(opts, anthropic.WithBaseURL(base))
			}
			return anthropic.New(key, opts...), nil
		},
		"openai": func(key string) (llm.ProviderAdapter, error) {
			var opts []openai.Option
			if base := os.Getenv("OPENAI_BASE_URL"); base != "" {
				opts = append(opts, openai.WithBaseURL(base))
			}
			return openai.New(key, opts...), nil
		},
		"google": func(key string) (llm.ProviderAdapter, error) {
			var opts []google.Option
			if base := os.Getenv("GEMINI_BASE_URL"); base != "" {
				opts = append(opts, google.WithBaseURL(base))
			}
			return google.New(key, opts...), nil
		},
	}
}

// writeJSON encodes v as JSON and writes it to w with a trailing newline.
func writeJSON(w io.Writer, v interface{}) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
