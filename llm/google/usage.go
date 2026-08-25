// ABOUTME: Translates Gemini usageMetadata into the unified llm.Usage shape.
// ABOUTME: Thinking sits outside the candidate count; cached content sits inside the prompt count.
package google

import "github.com/2389-research/tracker/llm"

type geminiUsageMeta struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
	// Billed at the output rate but NOT inside CandidatesTokenCount —
	// totalTokenCount = prompt + candidates + thoughts. Dropping it understated
	// the bill by however much the model thought.
	ThoughtsTokenCount int `json:"thoughtsTokenCount"`
	// The cached slice of the prompt, already counted inside PromptTokenCount.
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
}

// extractUsage converts Gemini usage metadata to the unified Usage struct,
// normalizing onto the llm.Usage invariants: thinking folds into OutputTokens
// (Gemini bills it there but omits it from candidatesTokenCount), and the cached
// prompt slice lifts out of PromptTokenCount into its own bucket so pricing can
// discount it without counting those tokens twice.
func extractUsage(meta *geminiUsageMeta) llm.Usage {
	if meta == nil {
		return llm.Usage{}
	}
	u := llm.Usage{
		InputTokens:  meta.PromptTokenCount - meta.CachedContentTokenCount,
		OutputTokens: meta.CandidatesTokenCount + meta.ThoughtsTokenCount,
	}
	if meta.ThoughtsTokenCount > 0 {
		v := meta.ThoughtsTokenCount
		u.ReasoningTokens = &v
	}
	if meta.CachedContentTokenCount > 0 {
		v := meta.CachedContentTokenCount
		u.CacheReadTokens = &v
	}
	// Derive the total from the normalized buckets (fresh input + output);
	// totalTokenCount folds in the cached prompt slice (SIFT-SUB-09-01).
	return u.Finalize()
}
