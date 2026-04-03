// ABOUTME: Fidelity modes control how much prior context gets injected into node prompts.
// ABOUTME: Provides parsing, degradation chain, resolution from attrs, and context compaction.
package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Fidelity represents a context fidelity level for pipeline execution.
type Fidelity string

const (
	// FidelityFull includes complete context from checkpoint (default for first execution).
	FidelityFull Fidelity = "full"
	// FidelitySummaryHigh includes all context keys plus trimmed artifact responses.
	FidelitySummaryHigh Fidelity = "summary:high"
	// FidelitySummaryMedium includes key decisions only: outcome, last_response, human_response.
	FidelitySummaryMedium Fidelity = "summary:medium"
	// FidelitySummaryLow includes one-line per completed node.
	FidelitySummaryLow Fidelity = "summary:low"
	// FidelityCompact includes only current task context, no prior node history.
	FidelityCompact Fidelity = "compact"
	// FidelityTruncate drops oldest context entries to fit a configurable token budget.
	FidelityTruncate Fidelity = "truncate"
)

// validFidelities is the set of recognized fidelity level strings.
var validFidelities = map[Fidelity]bool{
	FidelityFull:          true,
	FidelitySummaryHigh:   true,
	FidelitySummaryMedium: true,
	FidelitySummaryLow:    true,
	FidelityCompact:       true,
	FidelityTruncate:      true,
}

// degradeChain defines the degradation order from full to truncate.
var degradeChain = map[Fidelity]Fidelity{
	FidelityFull:          FidelitySummaryHigh,
	FidelitySummaryHigh:   FidelitySummaryMedium,
	FidelitySummaryMedium: FidelitySummaryLow,
	FidelitySummaryLow:    FidelityCompact,
	FidelityCompact:       FidelityTruncate,
	FidelityTruncate:      FidelityTruncate,
}

// mediumKeys are the context keys retained in summary:medium and truncate modes.
var mediumKeys = []string{
	ContextKeyOutcome,
	ContextKeyLastResponse,
	ContextKeyHumanResponse,
	ContextKeyPreferredLabel,
	ContextKeyToolStdout,
	ContextKeyToolStderr,
	ContextKeyGoal,
}

// ParseFidelity parses a string into a Fidelity value, returning an error for
// unrecognized values.
func ParseFidelity(s string) (Fidelity, error) {
	f := Fidelity(s)
	if !validFidelities[f] {
		return "", fmt.Errorf("unknown fidelity level: %q", s)
	}
	return f, nil
}

// DegradeFidelity returns the next lower fidelity level. Truncate is the floor
// and degrades to itself.
func DegradeFidelity(f Fidelity) Fidelity {
	if next, ok := degradeChain[f]; ok {
		return next
	}
	return FidelityTruncate
}

// ResolveFidelity determines the fidelity level for a node by checking the node
// attribute "fidelity", then the graph attribute "default_fidelity", then falling
// back to FidelityFull.
func ResolveFidelity(node *Node, graphAttrs map[string]string) Fidelity {
	if raw, ok := node.Attrs["fidelity"]; ok {
		if f, err := ParseFidelity(raw); err == nil {
			return f
		}
	}
	if raw, ok := graphAttrs["default_fidelity"]; ok {
		if f, err := ParseFidelity(raw); err == nil {
			return f
		}
	}
	return FidelityFull
}

// CompactContext reads the checkpoint context and optionally artifact files from
// disk, returning a compacted version based on the fidelity level.
func CompactContext(ctx *PipelineContext, completedNodes []string, fidelity Fidelity, artifactDir string, runID string) map[string]string {
	switch fidelity {
	case FidelityFull:
		return ctx.Snapshot()

	case FidelitySummaryHigh:
		return compactSummaryHigh(ctx, completedNodes, artifactDir, runID)

	case FidelitySummaryMedium:
		return compactMedium(ctx, false)

	case FidelitySummaryLow:
		return compactLow(ctx, completedNodes)

	case FidelityCompact:
		return compactCompact(ctx)

	case FidelityTruncate:
		return compactMedium(ctx, true)

	default:
		return ctx.Snapshot()
	}
}

// compactSummaryHigh returns all context keys plus trimmed artifact responses
// (first 2000 chars) for each completed node.
func compactSummaryHigh(ctx *PipelineContext, completedNodes []string, artifactDir string, runID string) map[string]string {
	result := ctx.Snapshot()

	for _, nodeID := range completedNodes {
		responsePath := filepath.Join(artifactDir, runID, nodeID, "response.md")
		data, err := os.ReadFile(responsePath)
		if err != nil {
			continue
		}
		content := string(data)
		if len(content) > 2000 {
			content = content[:2000]
		}
		result["summary."+nodeID] = content
	}

	return result
}

// DefaultTruncateLimit is the maximum character length for context values
// in truncate fidelity mode. Truncation is character-based (not token-based).
const DefaultTruncateLimit = 500

// truncateAtWordBoundary truncates s to approximately limit characters,
// cutting at the last word boundary before the limit. Appends "..." when
// truncation occurs.
func truncateAtWordBoundary(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := strings.LastIndex(s[:limit], " ")
	if cut <= 0 {
		cut = limit
	}
	return s[:cut] + "..."
}

// compactMedium returns only medium-fidelity keys. When truncate is true,
// each value is capped at DefaultTruncateLimit characters, cut at a word boundary.
func compactMedium(ctx *PipelineContext, truncate bool) map[string]string {
	result := make(map[string]string)
	for _, key := range mediumKeys {
		if val, ok := ctx.Get(key); ok {
			if truncate {
				val = truncateAtWordBoundary(val, DefaultTruncateLimit)
			}
			result[key] = val
		}
	}
	return result
}

// compactLow returns graph.goal plus a single completed_summary key with
// one line per completed node.
func compactLow(ctx *PipelineContext, completedNodes []string) map[string]string {
	result := make(map[string]string)
	if goal, ok := ctx.Get(ContextKeyGoal); ok {
		result[ContextKeyGoal] = goal
	}
	var lines []string
	for _, nodeID := range completedNodes {
		lines = append(lines, nodeID+": completed")
	}
	result["completed_summary"] = strings.Join(lines, "\n")
	return result
}

// compactCompact returns only graph.goal and outcome.
func compactCompact(ctx *PipelineContext) map[string]string {
	result := make(map[string]string)
	if goal, ok := ctx.Get(ContextKeyGoal); ok {
		result[ContextKeyGoal] = goal
	}
	if outcome, ok := ctx.Get(ContextKeyOutcome); ok {
		result[ContextKeyOutcome] = outcome
	}
	return result
}
