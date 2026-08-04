package pipeline

import (
	"sort"
	"strconv"

	"github.com/2389-research/tracker/llm"
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
	// unpricedModels is the set of uncatalogued model names that carried
	// billable usage. Membership is the run's Unpriced signal: usage priced at
	// an unknown rate defaulted to $0, so a --max-cost ceiling could not bound
	// it. Derived here from the model names already on the log lines rather than
	// threaded through the cost-event chain, because the two capture surfaces
	// (live cost_updated events and this post-hoc manifest) reach it by
	// different paths and both resolve to the same source of truth: whether the
	// model is in llm's catalog.
	unpricedModels map[string]struct{}
}

func newUsageLedger() *usageLedger {
	return &usageLedger{
		tiers:           map[usageTier]map[string]usageRecord{},
		callIDs:         map[string]struct{}{},
		pathCompletions: map[string]int{},
		unpricedModels:  map[string]struct{}{},
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
	l.recordIfUnpriced(e)
	tier, key := usageIdentity(e)
	bucket, ok := l.tiers[tier]
	if !ok {
		bucket = map[string]usageRecord{}
		l.tiers[tier] = bucket
	}
	rec := usageRecord{
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
	bucket[key] = keepKnownNode(rec, bucket[key])
}

// keepKnownNode carries a previously-recorded node id onto a replacement that
// lacks one.
//
// Both log paths share a call_id, but the client-sourced "finish" line carries
// no node_id while the agent-sourced "llm_finish" does. Plain last-writer-wins
// therefore let a node-less duplicate erase the attribution, after which
// nodeTotals drops the record: run totals stayed correct while the per-node
// shares silently stopped summing to the whole — the one invariant this file
// exists to hold.
func keepKnownNode(rec, prev usageRecord) usageRecord {
	if rec.nodeID == "" {
		rec.nodeID = prev.nodeID
	}
	return rec
}

// byNode regroups the per-tier buckets by node, keeping each node's records
// separated by tier. Tier selection is then made per node (see mostDirectTier),
// because backends do not all reach the same tier: a native node reports down to
// the call tier while a claude-code / ACP node reports only the node rollup. A
// single run-wide tier would pick the finest tier present anywhere and silently
// drop every node that never reaches it (#523). Records with no node id land
// under the empty key; they count toward run totals but not per-node
// attribution.
func (l *usageLedger) byNode() map[string]map[usageTier][]RunTotals {
	out := map[string]map[usageTier][]RunTotals{}
	for tier, bucket := range l.tiers {
		for _, r := range bucket {
			perTier, ok := out[r.nodeID]
			if !ok {
				perTier = map[usageTier][]RunTotals{}
				out[r.nodeID] = perTier
			}
			perTier[tier] = append(perTier[tier], r.totals)
		}
	}
	return out
}

// mostDirectTier returns one node's records from the most direct tier that
// reports the requested metric, so coarser restatements of the same calls are
// ignored. Tokens and cost are selected independently because no single tier
// carries both.
func mostDirectTier(perTier map[usageTier][]RunTotals, carries func(RunTotals) bool) []RunTotals {
	for _, t := range usageTiers {
		for _, r := range perTier[t] {
			if carries(r) {
				return perTier[t]
			}
		}
	}
	return nil
}

// runTotals sums the run's consumption. Each node contributes tokens and cost
// from its own most direct reporting tier, so a mixed-backend run counts every
// node instead of only those reaching a single run-wide tier.
func (l *usageLedger) runTotals(estimated bool) RunTotals {
	unpriced := l.sortedUnpricedModels()
	out := RunTotals{
		Estimated:      estimated,
		Unpriced:       len(unpriced) > 0,
		UnpricedModels: unpriced,
		LLMCalls:       l.llmCalls(),
	}
	for _, perTier := range l.byNode() {
		for _, r := range mostDirectTier(perTier, hasTokens) {
			addTokens(&out, r)
		}
		for _, r := range mostDirectTier(perTier, hasCost) {
			out.CostUSD += r.CostUSD
		}
	}
	return out
}

// recordIfUnpriced notes a usage-bearing line whose model has no catalog entry,
// so its tokens priced at $0. An empty model name (subscription-auth backends)
// is skipped — a --max-cost ceiling cannot bound it either, and it is not a
// misspelled-catalog signal. Only call for lines already known to carry usage.
func (l *usageLedger) recordIfUnpriced(e jsonlLogEntry) {
	if e.Model == "" || llm.IsPriced(e.Model) {
		return
	}
	l.unpricedModels[e.Model] = struct{}{}
}

// sortedUnpricedModels returns the uncatalogued model names seen, sorted for a
// stable manifest. Nil when none, so the omitempty JSON tag drops the field.
func (l *usageLedger) sortedUnpricedModels() []string {
	if len(l.unpricedModels) == 0 {
		return nil
	}
	out := make([]string, 0, len(l.unpricedModels))
	for m := range l.unpricedModels {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// nodeTotals attributes consumption to the node that incurred it, using the
// same per-node tier selection as the run totals so the parts sum to the whole.
// Records with no node id are omitted: they cannot be attributed, and guessing
// would silently misprice a node.
func (l *usageLedger) nodeTotals() map[string]*RunTotals {
	per := map[string]*RunTotals{}
	for nodeID, perTier := range l.byNode() {
		if nodeID == "" {
			continue
		}
		dst := &RunTotals{}
		for _, r := range mostDirectTier(perTier, hasTokens) {
			addTokens(dst, r)
		}
		for _, r := range mostDirectTier(perTier, hasCost) {
			dst.CostUSD += r.CostUSD
		}
		per[nodeID] = dst
	}
	return per
}

// addTokens accumulates every metered token figure, leaving cost alone.
func addTokens(dst *RunTotals, src RunTotals) {
	dst.InputTokens += src.InputTokens
	dst.OutputTokens += src.OutputTokens
	dst.CacheReadTokens += src.CacheReadTokens
	dst.CacheWriteTokens += src.CacheWriteTokens
	dst.ReasoningTokens += src.ReasoningTokens
}
