// ABOUTME: Tests for the --bypass-denylist, --tool-allowlist, and --max-output-limit CLI flags.
// ABOUTME: Covers flag parsing, defaults, repeatability, and propagation into the tool handler registry.
package main

import (
	"strings"
	"testing"

	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/pipeline/handlers"
)

// TestParseFlagsBypassDenylist verifies that --bypass-denylist sets cfg.bypassDenylist.
func TestParseFlagsBypassDenylist(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "--bypass-denylist", "pipeline.dip"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if !cfg.bypassDenylist {
		t.Fatal("expected bypassDenylist to be true")
	}
	if cfg.pipelineFile != "pipeline.dip" {
		t.Fatalf("pipelineFile = %q, want %q", cfg.pipelineFile, "pipeline.dip")
	}
}

// TestParseFlagsBypassDenylistDefault verifies the default is false (denylist active).
func TestParseFlagsBypassDenylistDefault(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "pipeline.dip"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.bypassDenylist {
		t.Fatal("expected bypassDenylist default to be false")
	}
}

// TestParseFlagsToolAllowlistSingle verifies one --tool-allowlist invocation populates the slice.
func TestParseFlagsToolAllowlistSingle(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "--tool-allowlist", "make *", "pipeline.dip"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if len(cfg.toolAllowlist) != 1 || cfg.toolAllowlist[0] != "make *" {
		t.Fatalf("toolAllowlist = %#v, want [%q]", cfg.toolAllowlist, "make *")
	}
}

// TestParseFlagsToolAllowlistRepeatable verifies multiple --tool-allowlist flags accumulate.
func TestParseFlagsToolAllowlistRepeatable(t *testing.T) {
	cfg, err := parseFlags([]string{
		"tracker",
		"--tool-allowlist", "make *",
		"--tool-allowlist", "go test *",
		"pipeline.dip",
	})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if len(cfg.toolAllowlist) != 2 {
		t.Fatalf("toolAllowlist len = %d, want 2: %#v", len(cfg.toolAllowlist), cfg.toolAllowlist)
	}
	if cfg.toolAllowlist[0] != "make *" || cfg.toolAllowlist[1] != "go test *" {
		t.Fatalf("toolAllowlist = %#v", cfg.toolAllowlist)
	}
}

// TestParseFlagsToolAllowlistCommaSeparated verifies comma-separated values within one flag.
func TestParseFlagsToolAllowlistCommaSeparated(t *testing.T) {
	cfg, err := parseFlags([]string{
		"tracker",
		"--tool-allowlist", "make *,go test *, echo *",
		"pipeline.dip",
	})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	want := []string{"make *", "go test *", "echo *"}
	if len(cfg.toolAllowlist) != len(want) {
		t.Fatalf("toolAllowlist = %#v, want %#v", cfg.toolAllowlist, want)
	}
	for i, p := range want {
		if cfg.toolAllowlist[i] != p {
			t.Fatalf("toolAllowlist[%d] = %q, want %q", i, cfg.toolAllowlist[i], p)
		}
	}
}

// TestParseFlagsToolAllowlistDefault verifies absent flag yields empty slice.
func TestParseFlagsToolAllowlistDefault(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "pipeline.dip"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if len(cfg.toolAllowlist) != 0 {
		t.Fatalf("toolAllowlist default = %#v, want empty", cfg.toolAllowlist)
	}
}

// TestParseFlagsMaxOutputLimit verifies --max-output-limit populates the int.
func TestParseFlagsMaxOutputLimit(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "--max-output-limit", "65536", "pipeline.dip"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.maxOutputLimit != 65536 {
		t.Fatalf("maxOutputLimit = %d, want 65536", cfg.maxOutputLimit)
	}
}

// TestParseFlagsMaxOutputLimitDefault verifies the default is 0 (meaning use built-in 10MB).
func TestParseFlagsMaxOutputLimitDefault(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "pipeline.dip"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.maxOutputLimit != 0 {
		t.Fatalf("maxOutputLimit default = %d, want 0 (library default)", cfg.maxOutputLimit)
	}
}

// TestParseFlagsMaxOutputLimitNegativeRejected verifies that a negative value is rejected.
func TestParseFlagsMaxOutputLimitNegativeRejected(t *testing.T) {
	_, err := parseFlags([]string{"tracker", "--max-output-limit", "-1", "pipeline.dip"})
	if err == nil {
		t.Fatal("expected error for negative --max-output-limit")
	}
}

// TestToolSafetyFlagsPropagateToActiveGlobal verifies executeRun copies the tool safety flag
// values into the activeToolSafety global so the registry picks them up.
func TestToolSafetyFlagsPropagateToActiveGlobal(t *testing.T) {
	t.Cleanup(func() { activeToolSafety = handlers.ToolHandlerConfig{} })

	var captured handlers.ToolHandlerConfig
	_ = executeCommand(runConfig{
		mode:           modeRun,
		pipelineFile:   "pipeline.dip",
		workdir:        "/tmp",
		noTUI:          true,
		bypassDenylist: true,
		toolAllowlist:  []string{"make *", "go test *"},
		maxOutputLimit: 131072,
	}, commandDeps{
		loadEnv: func(string) error { return nil },
		run: func(pipelineFile, workdir, checkpoint, format, backend string, verbose bool, jsonOut bool) error {
			captured = activeToolSafety
			return nil
		},
		runTUI: func(pipelineFile, workdir, checkpoint, format, backend string, verbose bool) error {
			t.Fatal("unexpected TUI path")
			return nil
		},
	})

	if !captured.BypassDenylist {
		t.Error("activeToolSafety.BypassDenylist not propagated")
	}
	if len(captured.Allowlist) != 2 || captured.Allowlist[0] != "make *" || captured.Allowlist[1] != "go test *" {
		t.Errorf("activeToolSafety.Allowlist = %#v, want [%q %q]", captured.Allowlist, "make *", "go test *")
	}
	if captured.MaxOutputLimit != 131072 {
		t.Errorf("activeToolSafety.MaxOutputLimit = %d, want 131072", captured.MaxOutputLimit)
	}
}

// TestToolHandlerConfigFlowsToRegistry drives the registry through the option wiring and
// verifies that the resulting tool handler reflects the flag values in a real end-to-end
// invocation: a denied command is blocked by default, permitted when bypass is active, and
// rejected by an allowlist when the command isn't listed.
func TestToolHandlerConfigFlowsToRegistry(t *testing.T) {
	graph := &pipeline.Graph{
		Nodes: map[string]*pipeline.Node{
			"node1": {
				ID:      "node1",
				Shape:   "parallelogram",
				Handler: "tool",
				Attrs:   map[string]string{"tool_command": "eval $(date)"},
			},
		},
	}
	env := exec.NewLocalEnvironment(t.TempDir())
	pctx := pipeline.NewPipelineContext()

	// Default-safe: denylist is active, "eval" pattern is blocked.
	reg := handlers.NewDefaultRegistry(graph,
		handlers.WithExecEnvironment(env),
	)
	h := mustGetToolHandler(t, reg)
	if _, err := h.Execute(t.Context(), graph.Nodes["node1"], pctx); err == nil {
		t.Fatal("expected default tool handler to block eval pattern")
	}

	// With --bypass-denylist: eval pattern goes through (actual exec may still fail for
	// other reasons in this synthetic test). What we assert is that the error, if any,
	// is NOT the denylist error.
	regBypass := handlers.NewDefaultRegistry(graph,
		handlers.WithExecEnvironment(env),
		handlers.WithToolHandlerConfig(handlers.ToolHandlerConfig{BypassDenylist: true}),
	)
	hBypass := mustGetToolHandler(t, regBypass)
	if _, err := hBypass.Execute(t.Context(), graph.Nodes["node1"], pctx); err != nil {
		if containsIgnoreCase(err.Error(), "denied pattern") {
			t.Fatalf("bypass should skip denylist, but got denylist error: %v", err)
		}
	}

	// With --tool-allowlist that does NOT match: blocked regardless of the command being
	// outside the denylist. Using "echo hi" which is safe, but not in the allowlist.
	graph.Nodes["node1"].Attrs["tool_command"] = "echo hi"
	regAllow := handlers.NewDefaultRegistry(graph,
		handlers.WithExecEnvironment(env),
		handlers.WithToolHandlerConfig(handlers.ToolHandlerConfig{
			Allowlist: []string{"make *"},
		}),
	)
	hAllow := mustGetToolHandler(t, regAllow)
	if _, err := hAllow.Execute(t.Context(), graph.Nodes["node1"], pctx); err == nil || !containsIgnoreCase(err.Error(), "allowlist") {
		t.Fatalf("expected allowlist rejection, got err=%v", err)
	}
}

// TestToolHandlerConfigMaxOutputLimitCap verifies that the hard ceiling overrides a larger
// per-node output_limit attr. We drive it via NewToolHandlerWithConfig directly since
// probing the internal cap requires inspecting clamped output rather than going through
// the registry (which hides the handler fields).
func TestToolHandlerConfigMaxOutputLimitCap(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	h := handlers.NewToolHandlerWithConfig(env, handlers.ToolHandlerConfig{
		MaxOutputLimit: 1024, // 1KB ceiling
	})
	node := &pipeline.Node{
		ID:      "big",
		Shape:   "parallelogram",
		Handler: "tool",
		Attrs: map[string]string{
			// Ask for 10KB via the node attr — should be clamped to 1KB by the handler.
			"tool_command": "yes x | head -c 10000",
			"output_limit": "10KB",
		},
	}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(t.Context(), node, pctx)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	stdout := outcome.ContextUpdates[pipeline.ContextKeyToolStdout]
	// With a 1KB ceiling, output + truncation marker should be well under the requested 10KB.
	if len(stdout) > 1024+128 {
		t.Errorf("stdout length = %d, expected <= ~1152 with 1KB ceiling", len(stdout))
	}
}

// mustGetToolHandler fetches the "tool" handler from a registry or fails the test.
func mustGetToolHandler(t *testing.T, reg *pipeline.HandlerRegistry) pipeline.Handler {
	t.Helper()
	h := reg.Get("tool")
	if h == nil {
		t.Fatal("registry missing tool handler")
	}
	return h
}

// containsIgnoreCase returns true when needle occurs in haystack, case-insensitive.
func containsIgnoreCase(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}
