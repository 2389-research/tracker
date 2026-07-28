package pipeline

import (
	"strconv"
)

// usageTier ranks how directly a log line reports LLM consumption.
//
// The same call is reported at up to three granularities: the call itself
// (llm_finish), the turn that issued it (turn_metrics), and the node that ran
// the turn (decision_outcome). Summing across tiers multiplies the truth — a
// six-turn node whose every call is restated at all three levels reports three
// times its real token count. So totals come from the most direct tier the log
// actually contains, and coarser restatements of the same calls are ignored.
//
// The tiers are ordered coarsest-first so that a higher value is more direct.
type usageTier int

const (
	tierNodeRollup usageTier = iota // decision_outcome: one line per node
	tierTurn                        // turn_metrics: one line per agent turn
	tierCall                        // llm_finish: one line per LLM call
)

// usageTiers lists the tiers most-direct first, which is the order totals
// prefer them in.
var usageTiers = []usageTier{tierCall, tierTurn, tierNodeRollup}

// usageRecord is one tier's report of consumption, tagged with the node it
// belongs to so per-node attribution falls out of the same pass.
type usageRecord struct {
	nodeID string
	totals RunTotals
}

// usageIdentity classifies a usage-bearing line and names the unit it
// describes, so repeated reports of one unit collapse instead of summing.
//
// Only the call tier can repeat within itself, because both log paths report
// the same call; call_id collapses that pair. The turn and node tiers emit once
// per turn and once per node visit, so each of their lines is its own unit — a
// retried or revisited node emits a separate rollup per visit, and keying those
// by node id alone would silently discard every visit but the last.
func usageIdentity(e jsonlLogEntry) (usageTier, string) {
	perLine := e.Timestamp + "|" + e.Type + "|" + e.NodeID
	switch e.Type {
	case "decision_outcome":
		return tierNodeRollup, perLine
	case "turn_metrics":
		return tierTurn, perLine + "|" + e.SessionID + "|" + strconv.Itoa(e.TurnNo)
	default:
		if e.CallID != "" {
			return tierCall, e.CallID
		}
		return tierCall, perLine
	}
}

// hasTokens and hasCost say which metric a record carries. Tokens and cost are
// selected independently because no single tier carries both: llm_finish meters
// tokens without pricing them, and turn_metrics prices a turn without
// re-reporting per-call cache figures.
func hasTokens(r RunTotals) bool { return r.InputTokens != 0 || r.OutputTokens != 0 }
func hasCost(r RunTotals) bool   { return r.CostUSD != 0 }

// isCompletion reports whether a line marks one LLM call finishing.
//
// Both log paths record the same call: the client writer emits "finish" for
// every call, and an agent session re-emits its own subset as "llm_finish".
// Counting either alone is wrong — in archived runs the agent subset was under
// half the total — so both are counted and deduplicated by call identity.
func isCompletion(e jsonlLogEntry) bool {
	if e.Source != "llm" && e.Source != "agent" {
		return false
	}
	return e.Type == "finish" || e.Type == "llm_finish"
}

// usageLedger folds usage-bearing log lines into per-tier buckets.
type usageLedger struct {
	tiers map[usageTier]map[string]usageRecord
	// callIDs holds one entry per distinct call_id seen completing. Counted
	// from completion events rather than from usage rows: the tier carrying
	// usage is often coarser than a call, so its entry count is not a call
	// count.
	callIDs map[string]struct{}
	// pathCompletions counts completions per log path, for logs written
	// before call_id existed. Timestamps cannot substitute — the two paths
	// record the same call microseconds apart, so pairing by timestamp
	// matched barely a tenth of one archived run's calls and nearly doubled
	// its total.
	pathCompletions map[string]int
}

func newUsageLedger() *usageLedger {
	return &usageLedger{
		tiers:           map[usageTier]map[string]usageRecord{},
		callIDs:         map[string]struct{}{},
		pathCompletions: map[string]int{},
	}
}

// llmCalls returns the number of distinct LLM calls.
//
// Each path independently tries to record every call, so the fuller path is
// the better count and a union across paths would double-count. Where call_ids
// are present they identify calls exactly and agree with the per-path counts;
// where they are absent the per-path maximum carries it, which is a lower bound
// if one path dropped events.
func (l *usageLedger) llmCalls() int {
	best := len(l.callIDs)
	for _, n := range l.pathCompletions {
		if n > best {
			best = n
		}
	}
	return best
}

// add records one line, replacing any earlier report of the same logical unit:
// a finish event carries that unit's authoritative totals and supersedes
// partial figures reported while it was still streaming.
func (l *usageLedger) add(e jsonlLogEntry) {
	if isCompletion(e) {
		l.pathCompletions[e.Source]++
		if e.CallID != "" {
			l.callIDs[e.CallID] = struct{}{}
		}
	}
	if e.TokenInput == 0 && e.TokenOutput == 0 && e.EstimatedCost == 0 {
		return
	}
	tier, key := usageIdentity(e)
	bucket, ok := l.tiers[tier]
	if !ok {
		bucket = map[string]usageRecord{}
		l.tiers[tier] = bucket
	}
	bucket[key] = usageRecord{
		nodeID: e.NodeID,
		totals: RunTotals{
			InputTokens:      e.TokenInput,
			OutputTokens:     e.TokenOutput,
			CacheReadTokens:  e.CacheReadTokens,
			CacheWriteTokens: e.CacheWriteTokens,
			ReasoningTokens:  e.ReasoningTokens,
			CostUSD:          e.EstimatedCost,
		},
	}
}

// bestTier returns the most direct tier holding the given metric.
func (l *usageLedger) bestTier(carries func(RunTotals) bool) (usageTier, bool) {
	for _, t := range usageTiers {
		for _, r := range l.tiers[t] {
			if carries(r.totals) {
				return t, true
			}
		}
	}
	return tierCall, false
}

// runTotals sums the run's consumption, taking tokens and cost each from the
// most direct tier reporting them.
func (l *usageLedger) runTotals(estimated bool) RunTotals {
	out := RunTotals{Estimated: estimated, LLMCalls: l.llmCalls()}
	if t, ok := l.bestTier(hasTokens); ok {
		for _, r := range l.tiers[t] {
			addTokens(&out, r.totals)
		}
	}
	if t, ok := l.bestTier(hasCost); ok {
		for _, r := range l.tiers[t] {
			out.CostUSD += r.totals.CostUSD
		}
	}
	return out
}

// nodeTotals attributes consumption to the node that incurred it, using the
// same tier selection as the run totals so the parts sum to the whole.
// Records with no node id are omitted: they cannot be attributed, and guessing
// would silently misprice a node.
func (l *usageLedger) nodeTotals() map[string]*RunTotals {
	per := map[string]*RunTotals{}
	if t, ok := l.bestTier(hasTokens); ok {
		l.foldNode(per, t, func(dst *RunTotals, src RunTotals) { addTokens(dst, src) })
	}
	if t, ok := l.bestTier(hasCost); ok {
		l.foldNode(per, t, func(dst *RunTotals, src RunTotals) { dst.CostUSD += src.CostUSD })
	}
	return per
}

// foldNode applies one tier's records to their nodes.
func (l *usageLedger) foldNode(per map[string]*RunTotals, t usageTier, apply func(*RunTotals, RunTotals)) {
	for _, r := range l.tiers[t] {
		if r.nodeID == "" {
			continue
		}
		dst, ok := per[r.nodeID]
		if !ok {
			dst = &RunTotals{}
			per[r.nodeID] = dst
		}
		apply(dst, r.totals)
	}
}

// addTokens accumulates every metered token figure, leaving cost alone.
func addTokens(dst *RunTotals, src RunTotals) {
	dst.InputTokens += src.InputTokens
	dst.OutputTokens += src.OutputTokens
	dst.CacheReadTokens += src.CacheReadTokens
	dst.CacheWriteTokens += src.CacheWriteTokens
	dst.ReasoningTokens += src.ReasoningTokens
}
