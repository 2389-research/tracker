// ABOUTME: Pipeline source parsing for the library entry points (.dip / DOT detection and load).
// ABOUTME: Routes a built-in's own text to the embed FS so its *_file sidecars resolve without disk.
package tracker

import (
	"fmt"
	"strings"

	"github.com/2389-research/dippin-lang/validator"
	"github.com/2389-research/tracker/internal/diag"
	"github.com/2389-research/tracker/pipeline"
)

// parsePipelineSource parses a pipeline source string using the given format.
// If format is empty, auto-detects: DOT sources start with "digraph" or
// "strict digraph"; everything else is treated as .dip.
func parsePipelineSource(source, format string) (*pipeline.Graph, error) {
	if format == "" {
		format = detectSourceFormat(source)
	}

	switch format {
	case "dot":
		return parseDOTSource(source)
	case "dip":
		return parseDIPSource(source)
	default:
		return nil, fmt.Errorf("unknown format %q (valid: dip, dot)", format)
	}
}

// detectSourceFormat returns "dot" for DOT-syntax sources and "dip" otherwise.
func detectSourceFormat(source string) string {
	trimmed := strings.TrimSpace(source)
	if strings.HasPrefix(trimmed, "digraph") || strings.HasPrefix(trimmed, "strict digraph") {
		return "dot"
	}
	return "dip"
}

// parseDOTSource parses a DOT-format pipeline source.
func parseDOTSource(source string) (*pipeline.Graph, error) {
	diag.Warnf("WARNING: DOT format is deprecated. Migrate pipelines to .dip format.")
	graph, err := pipeline.ParseDOT(source)
	if err != nil {
		return nil, fmt.Errorf("parse DOT: %w", err)
	}
	return graph, nil
}

// parseDIPSource parses a Dippin-format pipeline source, runs validation and lint.
// A source byte-identical to an embedded built-in (the text ResolveSource /
// OpenWorkflow hand back) resolves its *_file directives from the embed FS;
// any other source resolves them relative to the process cwd ("inline.dip").
func parseDIPSource(source string) (*pipeline.Graph, error) {
	return parseDIPSourceAt(source, inlineSourceName)
}

// parseDIPSourceAt is parseDIPSource with an explicit anchor: directives
// resolve from disk relative to filepath.Dir(filename), so a caller that read
// the source from a known file gets its sidecars from the file's own directory
// instead of cwd (and never from the embed FS — a file on disk is the
// author's copy, edits included).
func parseDIPSourceAt(source, filename string) (*pipeline.Graph, error) {
	graph, diags, err := loadDIPSource(source, filename)
	// Log validation errors and lint warnings before returning so callers
	// see the specific diagnostics even on fatal failures.
	for _, d := range diags {
		diag.Warnf("%s", d.String())
	}
	if err != nil {
		return nil, err
	}
	return graph, nil
}

// inlineSourceName is the synthetic filename for a source string with no
// known on-disk location; its *_file directives resolve relative to cwd.
const inlineSourceName = "inline.dip"

// loadDIPSource routes a .dip source to the right directive resolver. A
// source with a known on-disk location (filename other than inlineSourceName)
// always resolves from disk next to that file. A location-less source that is
// byte-identical to a built-in resolves from the embed FS; any other
// location-less source resolves from cwd via dippin's disk resolver.
func loadDIPSource(source, filename string) (*pipeline.Graph, []validator.Diagnostic, error) {
	if filename == inlineSourceName {
		if info, ok := embeddedWorkflowForSource(source); ok {
			return pipeline.LoadDippinWorkflowFS(source, info.File, embeddedWorkflows)
		}
	}
	return pipeline.LoadDippinWorkflow(source, filename)
}
