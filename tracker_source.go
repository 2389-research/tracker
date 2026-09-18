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

// SourceRef says where a pipeline source string came from, so its *_file
// directives (prompt_file / command_file / system_prompt_file /
// prompt_include and the defaults cascade files) resolve against the right
// location. Exactly one of the fields is normally set; both empty means the
// origin is unknown.
//
//   - Path: the on-disk file the source was read from. Directives resolve
//     from disk relative to its directory, so a `tracker init` copy — edited
//     sidecars included — loads the same from any cwd.
//   - Builtin: the bare name of an embedded built-in (a Workflows() entry).
//     Directives resolve inside the embed FS; no disk is consulted.
//   - Neither: as a last resort, a source byte-identical to a built-in is
//     treated as that built-in (so an embedder passing raw OpenWorkflow text
//     still works); anything else resolves relative to the process cwd. Prefer
//     an explicit ref — ResolveSource returns one via WorkflowInfo.Ref().
type SourceRef struct {
	Path    string
	Builtin string
}

// SourceOption configures the read-only source entry points (Simulate,
// EstimateRun, DescribeInputs).
type SourceOption func(*sourceConfig)

type sourceConfig struct {
	ref SourceRef
}

// WithSource anchors a source string for Simulate / EstimateRun /
// DescribeInputs — see SourceRef.
func WithSource(ref SourceRef) SourceOption {
	return func(c *sourceConfig) { c.ref = ref }
}

func applySourceOptions(opts []SourceOption) sourceConfig {
	var c sourceConfig
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// ParseSource parses a pipeline source string into a *pipeline.Graph without
// constructing an engine, resolving *_file sidecars per the anchored
// SourceRef exactly as Run / NewEngine do. It is the seam for an embedder
// that must hold and MUTATE the graph before NewEngineFromGraph — per-node
// model tiering, a forced provider, attr injection — for a source whose
// sidecars are not on disk: with WithSource(SourceRef{Builtin: name}) the
// prompt_file / command_file directives resolve from the embed FS and the
// graph is marked as that built-in, so NewEngineFromGraph materializes its
// ${graph.workflow_dir} tree. Parsing the raw text with
// pipeline.LoadDippinWorkflow instead fails on the first prompt_file.
//
// format is "dip", "dot" (deprecated) or "" to auto-detect. Validation and
// lint diagnostics are logged, and a validation error is returned as err.
func ParseSource(source, format string, opts ...SourceOption) (*pipeline.Graph, error) {
	sc := applySourceOptions(opts)
	return parsePipelineSource(source, format, sc.ref)
}

// parsePipelineSource parses a pipeline source string using the given format.
// If format is empty, auto-detects: DOT sources start with "digraph" or
// "strict digraph"; everything else is treated as .dip. ref anchors .dip
// directive resolution (see SourceRef); the zero value means "unknown origin".
func parsePipelineSource(source, format string, ref SourceRef) (*pipeline.Graph, error) {
	if format == "" {
		format = detectSourceFormat(source)
	}

	switch format {
	case "dot":
		return parseDOTSource(source)
	case "dip":
		return parseDIPSource(source, ref)
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

// parseDIPSource parses a Dippin-format pipeline source, runs validation and
// lint, resolving *_file directives per ref (see SourceRef).
func parseDIPSource(source string, ref SourceRef) (*pipeline.Graph, error) {
	graph, diags, err := loadDIPSource(source, ref)
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
// known origin; its *_file directives resolve relative to cwd.
const inlineSourceName = "inline.dip"

// loadDIPSource routes a .dip source to the directive resolver SourceRef
// selects: Path → disk next to the file; Builtin → the embed FS; neither →
// the byte-identical-to-a-built-in fallback, then disk relative to cwd.
//
// A Path load also seeds ${graph.workflow_dir} to the file's directory (#332),
// so a library caller passing a Path gets the same value the CLI seeds; a
// built-in is marked with WorkflowBuiltinAttr instead and gets its
// workflow_dir materialized at engine construction (tracker_workflow_dir.go).
// An unknown-origin source gets neither.
func loadDIPSource(source string, ref SourceRef) (*pipeline.Graph, []validator.Diagnostic, error) {
	switch {
	case ref.Path != "":
		graph, diags, err := pipeline.LoadDippinWorkflow(source, ref.Path)
		if err == nil {
			pipeline.SeedWorkflowDir(graph, ref.Path)
		}
		return graph, diags, err
	case ref.Builtin != "":
		info, ok := LookupWorkflow(ref.Builtin)
		if !ok {
			return nil, nil, fmt.Errorf("no built-in workflow named %q", ref.Builtin)
		}
		return loadBuiltinDIPSource(source, info)
	}
	if info, ok := embeddedWorkflowForSource(source); ok {
		return loadBuiltinDIPSource(source, info)
	}
	return pipeline.LoadDippinWorkflow(source, inlineSourceName)
}

// loadBuiltinDIPSource loads a built-in's text with its sidecars resolved
// from the embed FS and marks the graph as that built-in.
func loadBuiltinDIPSource(source string, info WorkflowInfo) (*pipeline.Graph, []validator.Diagnostic, error) {
	graph, diags, err := pipeline.LoadDippinWorkflowFS(source, info.File, embeddedWorkflows)
	if err == nil {
		markBuiltinWorkflow(graph, info)
	}
	return graph, diags, err
}
