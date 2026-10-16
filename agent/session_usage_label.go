// ABOUTME: The provider labels a session's usage is accounted under: the client's label for the configured
// ABOUTME: provider (openai-subscription on a ChatGPT subscription), and per response once a failover lane serves.
package agent

import "github.com/2389-research/tracker/llm"

// usageLabeler is the optional usage-label capability of the concrete LLM
// client (*llm.Client). Type-asserted so the base Completer interface stays
// minimal and test stubs need not implement it.
type usageLabeler interface {
	UsageLabel(provider string) string
}

// The concrete client must provide it, so real runs always get the label.
var _ usageLabeler = (*llm.Client)(nil)

// usageLabel returns the provider name SessionResult.Provider carries, which
// session stats and the trace's per-provider usage totals bucket under: the
// client's label for the configured provider (openai-subscription when openai
// runs on a ChatGPT subscription, priced at $0), or the configured provider
// when the client gives no label. Routing never reads it; requests still name
// s.config.Provider.
func (s *Session) usageLabel() string {
	if labeler, ok := s.client.(usageLabeler); ok {
		if label := labeler.UsageLabel(s.config.Provider); label != "" {
			return label
		}
	}
	return s.config.Provider
}

// addUsage adds resp's usage to result, priced by the provider and model that
// served it. A response under a label other than the session's — a failover
// lane (#486) — starts result.UsageByProvider, seeded with what the session's
// label had served, so per-provider rollups file every turn under the lane
// that served it (#702).
func (s *Session) addUsage(result *SessionResult, resp *llm.Response) {
	usage := resp.Usage
	if usage.EstimatedCost == 0 {
		usage.EstimatedCost = llm.EstimateCostForProvider(s.pricingProvider(resp), s.pricingModel(resp), resp.Usage)
	}
	label := resp.Provider
	if label == "" {
		label = result.Provider
	}
	if result.UsageByProvider == nil && label != result.Provider {
		result.UsageByProvider = make(map[string]llm.Usage)
		if hasUsage(result.Usage) {
			result.UsageByProvider[result.Provider] = result.Usage
		}
	}
	if result.UsageByProvider != nil {
		result.UsageByProvider[label] = result.UsageByProvider[label].Add(usage)
	}
	result.Usage = result.Usage.Add(usage)
}

// hasUsage reports whether u counts any tokens or cost. Cache reads and writes
// are their own buckets, outside TotalTokens, so a cache-only response on a
// model with no price still counts.
func hasUsage(u llm.Usage) bool {
	return u.TotalTokens > 0 || u.EstimatedCost > 0 || positive(u.CacheReadTokens) || positive(u.CacheWriteTokens)
}

// positive reports whether n is set and above zero.
func positive(n *int) bool {
	return n != nil && *n > 0
}
