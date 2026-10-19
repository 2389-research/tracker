// ABOUTME: Which backends a pipeline needs, for tracker doctor (#716): the provider-key
// ABOUTME: check passes when no node needs native, and claude is required when a node needs it.
package tracker

import (
	"context"
	"os"
	"strings"

	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/pipeline/handlers"
)

// WithDoctorGraphs gives Doctor the already-loaded pipeline graph and its
// subgraph children, so the backend checks see every agent node. Without it
// Doctor parses the top-level PipelineFile itself (no subgraphs).
func WithDoctorGraphs(graph *pipeline.Graph, subgraphs map[string]*pipeline.Graph) DoctorOption {
	return func(cfg *DoctorConfig) {
		cfg.graph = graph
		cfg.subgraphs = subgraphs
	}
}

// doctorBackendNeeds reports which backends the run will need: from the
// pipeline's agent nodes when one is known, else from --backend alone (no
// backend means native, as today).
func doctorBackendNeeds(cfg DoctorConfig) pipeline.BackendNeeds {
	g, subgraphs := cfg.graph, cfg.subgraphs
	if g == nil && cfg.PipelineFile != "" {
		g = parseDoctorPipeline(cfg.PipelineFile)
	}
	if g != nil {
		return pipeline.ComputeBackendNeeds(g, subgraphs, cfg.Backend)
	}
	switch cfg.Backend {
	case "claude-code":
		return pipeline.BackendNeeds{ClaudeCode: true}
	case "acp":
		return pipeline.BackendNeeds{ACP: true}
	}
	return pipeline.BackendNeeds{Native: true}
}

// parseDoctorPipeline best-effort parses a .dip/.dot file or a built-in name;
// nil when it can't (the Pipeline File check reports why).
func parseDoctorPipeline(name string) *pipeline.Graph {
	if _, err := os.Stat(name); err != nil {
		if info, ok := builtinForDoctor(name); ok {
			g, _ := loadEmbeddedGraph(info)
			return g
		}
		return nil
	}
	if strings.EqualFold(name[strings.LastIndex(name, ".")+1:], "dipx") {
		return nil
	}
	src, err := os.ReadFile(name)
	if err != nil {
		return nil
	}
	g, _ := parsePipelineFile(string(src), name)
	return g
}

// providersNotNeeded is the LLM Providers result when no node needs a native
// client. ok is false when the provider check must run: some node needs
// native, or an auto node would fall back to native because claude is
// unavailable.
func providersNotNeeded(needs pipeline.BackendNeeds, claudeOK bool) (CheckResult, bool) {
	if needs.NeedsNativeClient() || (needs.Auto && !claudeOK) {
		return CheckResult{}, false
	}
	msg := "not needed by this pipeline (no agent nodes)"
	switch {
	case needs.ClaudeCode || needs.Auto:
		msg = "not needed by this pipeline (runs on Claude Code)"
	case needs.ACP:
		msg = "not needed by this pipeline (runs on ACP agents)"
	}
	return CheckResult{Name: "LLM Providers", Status: CheckStatusOK, Message: msg}, true
}

// autoNeedsKeyHint is appended when an auto node will fall back to native.
const autoNeedsKeyHint = "; or install Claude Code and run `claude /login` — backend: auto nodes then need no API key"

func claudeAvailableFor(ctx context.Context, cfg DoctorConfig) bool {
	_, _, ok := handlers.ClaudeAvailable(ctx, cfg.WorkDir)
	return ok
}
