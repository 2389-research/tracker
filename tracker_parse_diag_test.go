// ABOUTME: Regression tests that root-package parse/preflight diagnostics route to the diag sink (#449).
package tracker

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// TestParseDIPSourceDiagnosticsRouteToSink pins the #449 contract for the most
// common library entry path: lint/validation diagnostics emitted while parsing
// a .dip workflow must reach the injected sink, not the process-global logger.
func TestParseDIPSourceDiagnosticsRouteToSink(t *testing.T) {
	var buf bytes.Buffer
	SetDiagnosticLogger(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { SetDiagnosticLogger(nil) })

	// A syntactically valid workflow that fails validation (no start/exit)
	// deterministically yields diagnostics via pipeline.LoadDippinWorkflow.
	src := "pipeline mini {\n  node A [shape=box]\n  node B [shape=box]\n  A -> B\n}\n"
	_, _ = parseDIPSource(src)

	got := buf.String()
	if !strings.Contains(got, "DIP001") && !strings.Contains(got, "no start node") {
		t.Fatalf("injected sink did not receive the parse diagnostic; got %q", got)
	}
}

// TestParseDOTSourceDeprecationRoutesToSink pins that the DOT-deprecation
// warning routes to the injected sink instead of the global logger.
func TestParseDOTSourceDeprecationRoutesToSink(t *testing.T) {
	var buf bytes.Buffer
	SetDiagnosticLogger(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { SetDiagnosticLogger(nil) })

	_, _ = parseDOTSource("digraph { A -> B }")

	got := buf.String()
	if !strings.Contains(got, "DOT format is deprecated") {
		t.Fatalf("injected sink did not receive the deprecation diagnostic; got %q", got)
	}
}

// TestRunPreflightWarningRoutesToSink pins that library preflight warnings route
// to the injected sink instead of os.Stderr.
func TestRunPreflightWarningRoutesToSink(t *testing.T) {
	var buf bytes.Buffer
	SetDiagnosticLogger(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { SetDiagnosticLogger(nil) })

	// requires:git + warn policy + a non-git workdir -> warns and returns nil.
	graph := &pipeline.Graph{Attrs: map[string]string{"requires": "git"}}
	cfg := Config{Git: &GitConfig{Preflight: GitPreflightWarn}}
	workDir := t.TempDir()

	if err := runPreflight(context.Background(), graph, cfg, workDir); err != nil {
		t.Fatalf("expected warn policy to return nil, got %v", err)
	}

	if buf.Len() == 0 {
		t.Fatalf("injected sink did not receive the preflight warning; got empty output")
	}
}
