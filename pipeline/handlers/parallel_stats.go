// ABOUTME: Sums a parallel node's branch SessionStats into the node's own stats, keeping each
// ABOUTME: branch's provider attribution and session count for the usage rollup (#711).
package handlers

import "github.com/2389-research/tracker/pipeline"

// aggregateBranchStats sums SessionStats from all parallel branch results,
// keeping the branches' provider attribution and session count (#711).
// Returns nil if no branches produced stats.
func aggregateBranchStats(results []ParallelResult) *pipeline.SessionStats {
	var agg *pipeline.SessionStats
	var branches []*pipeline.SessionStats
	for _, r := range results {
		if r.Stats == nil {
			continue
		}
		if agg == nil {
			agg = &pipeline.SessionStats{ToolCalls: make(map[string]int)}
		}
		mergeSessionStats(agg, r.Stats)
		branches = append(branches, r.Stats)
	}
	if agg != nil {
		agg.AttributeBranches(branches)
	}
	return agg
}

// mergeSessionStats adds src fields into dst in-place. OR-propagates
// Estimated so that a heuristic-derived branch (e.g. ACP-backed) taints
// the aggregated parallel-node stats — otherwise downstream surfaces
// would render mixed metered+estimated parallel output as fully metered.
// EstimateSource is carried forward from the first estimated contributor;
// a later metered branch doesn't clear it.
func mergeSessionStats(dst, src *pipeline.SessionStats) {
	dst.Turns += src.Turns
	dst.TotalToolCalls += src.TotalToolCalls
	dst.InputTokens += src.InputTokens
	dst.OutputTokens += src.OutputTokens
	dst.TotalTokens += src.TotalTokens
	dst.CostUSD += src.CostUSD
	dst.Compactions += src.Compactions
	dst.CacheHits += src.CacheHits
	dst.CacheMisses += src.CacheMisses
	if src.LongestTurn > dst.LongestTurn {
		dst.LongestTurn = src.LongestTurn
	}
	dst.FilesModified = append(dst.FilesModified, src.FilesModified...)
	dst.FilesCreated = append(dst.FilesCreated, src.FilesCreated...)
	for name, count := range src.ToolCalls {
		dst.ToolCalls[name] += count
	}
	if src.Estimated {
		dst.Estimated = true
		if dst.EstimateSource == "" {
			dst.EstimateSource = src.EstimateSource
		}
	}
}
