// ABOUTME: Translates OpenAI Responses API usage into the unified llm.Usage shape.
// ABOUTME: Both cache classes and reasoning arrive nested inside the top-level counts.
package openai

import "github.com/2389-research/tracker/llm"

type openaiUsage struct {
	InputTokens  int              `json:"input_tokens"`
	OutputTokens int              `json:"output_tokens"`
	TotalTokens  int              `json:"total_tokens"`
	OutputDetail *openaiOutDetail `json:"output_tokens_details,omitempty"`
	InputDetail  *openaiInDetail  `json:"input_tokens_details,omitempty"`
}

type openaiOutDetail struct {
	// Already counted inside OutputTokens.
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

// openaiInDetail breaks down InputTokens. Both fields are nested *inside* it and
// billed at their own rate, so both are lifted out — see translateUsage.
type openaiInDetail struct {
	CachedTokens     int `json:"cached_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

// translateUsage converts OpenAI usage to unified format.
//
// OutputTokens already includes reasoning — the llm.Usage invariant — so it
// passes through untouched. The cache buckets arrive nested inside InputTokens
// and are lifted out: pricing sums the buckets, so leaving them in charges the
// full rate for discounted tokens, while moving them without subtracting counts
// the prompt twice.
func translateUsage(u openaiUsage) llm.Usage {
	usage := llm.Usage{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		TotalTokens:  u.TotalTokens,
	}
	if u.OutputDetail != nil && u.OutputDetail.ReasoningTokens > 0 {
		v := u.OutputDetail.ReasoningTokens
		usage.ReasoningTokens = &v
	}
	if u.InputDetail != nil && u.InputDetail.CachedTokens > 0 {
		v := u.InputDetail.CachedTokens
		usage.InputTokens -= v
		usage.CacheReadTokens = &v
	}
	if u.InputDetail != nil && u.InputDetail.CacheWriteTokens > 0 {
		v := u.InputDetail.CacheWriteTokens
		usage.InputTokens -= v
		usage.CacheWriteTokens = &v
	}
	// Floor at zero: a gateway reporting a non-nested (or double-counted) write
	// bucket must not drive the uncached remainder negative, which would credit
	// phantom input tokens back against the bill.
	if usage.InputTokens < 0 {
		usage.InputTokens = 0
	}
	return usage
}
