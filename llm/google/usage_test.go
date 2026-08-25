// ABOUTME: Tests that Gemini usage metadata normalizes onto the llm.Usage invariants.
// ABOUTME: Thinking tokens are billed but excluded from the candidate count; cached prompt tokens are nested inside it.
package google

import "testing"

// TestExtractUsageFoldsThinkingIntoOutput pins the arithmetic that a live run
// exposed: Gemini reports totalTokenCount = prompt + candidates + thoughts, so
// thinking tokens are billed at the output rate while sitting outside
// candidatesTokenCount. Dropping them understated the bill by however much the
// model thought — on a reasoning-heavy call that is nearly all of the output.
func TestExtractUsageFoldsThinkingIntoOutput(t *testing.T) {
	// Shape taken from a real gemini-3-flash-preview response.
	u := extractUsage(&geminiUsageMeta{
		PromptTokenCount:     2,
		CandidatesTokenCount: 1,
		ThoughtsTokenCount:   44,
		TotalTokenCount:      47,
	})

	if u.OutputTokens != 45 {
		t.Errorf("OutputTokens = %d, want 45 (1 candidate + 44 thinking)", u.OutputTokens)
	}
	if u.InputTokens != 2 {
		t.Errorf("InputTokens = %d, want 2", u.InputTokens)
	}
	if u.ReasoningTokens == nil || *u.ReasoningTokens != 44 {
		t.Errorf("ReasoningTokens = %v, want 44 recorded for visibility", u.ReasoningTokens)
	}
	// The invariant that makes cost right: nothing Gemini billed is unaccounted.
	if got := u.InputTokens + u.OutputTokens; got != u.TotalTokens {
		t.Errorf("input+output = %d but Gemini reported total %d; the difference is billed and unpriced",
			got, u.TotalTokens)
	}
}

// TestExtractUsageLiftsCachedPromptOut covers the opposite nesting: the cached
// slice is already inside promptTokenCount, and pricing sums the buckets, so it
// has to be subtracted on the way into CacheReadTokens or the prompt is counted
// twice.
func TestExtractUsageLiftsCachedPromptOut(t *testing.T) {
	u := extractUsage(&geminiUsageMeta{
		PromptTokenCount:        1000, // includes the 600 cached
		CachedContentTokenCount: 600,
		CandidatesTokenCount:    50,
		TotalTokenCount:         1050,
	})

	if u.InputTokens != 400 {
		t.Errorf("InputTokens = %d, want 400 (1000 prompt - 600 cached)", u.InputTokens)
	}
	if u.CacheReadTokens == nil || *u.CacheReadTokens != 600 {
		t.Errorf("CacheReadTokens = %v, want 600", u.CacheReadTokens)
	}
	// Buckets must reconstitute the prompt exactly.
	if got := u.InputTokens + *u.CacheReadTokens; got != 1000 {
		t.Errorf("input+cacheRead = %d, want the 1000 tokens Gemini reported", got)
	}
	// SIFT-SUB-09-01: the normalized total is fresh input + output, never the
	// reported totalTokenCount (which folds the cached slice back in). Here that
	// is 400 fresh input + 50 output = 450, not Gemini's reported 1050.
	if u.TotalTokens != 450 {
		t.Errorf("TotalTokens = %d, want 450 (400 fresh input + 50 output, cache read excluded)", u.TotalTokens)
	}
}

// TestUsageFromMetaMatchesExtractUsage guards the streaming and non-streaming
// paths against drifting apart. They held duplicate conversions, and the copy
// in the adapter is what let thinking tokens go unpriced on streamed calls —
// which is every call an agent session makes.
func TestUsageFromMetaMatchesExtractUsage(t *testing.T) {
	meta := &geminiUsageMeta{
		PromptTokenCount:        900,
		CachedContentTokenCount: 100,
		CandidatesTokenCount:    20,
		ThoughtsTokenCount:      30,
		TotalTokenCount:         950,
	}
	streamed, direct := usageFromMeta(meta), extractUsage(meta)
	if streamed == nil {
		t.Fatal("usageFromMeta returned nil for non-nil metadata")
	}
	// Compared field by field: Usage holds pointers for the optional buckets,
	// so struct equality would compare addresses and never match.
	if streamed.InputTokens != direct.InputTokens ||
		streamed.OutputTokens != direct.OutputTokens ||
		streamed.TotalTokens != direct.TotalTokens {
		t.Errorf("token counts differ: streaming %s, non-streaming %s", streamed, direct)
	}
	for _, c := range []struct {
		name             string
		streamed, direct *int
	}{
		{"ReasoningTokens", streamed.ReasoningTokens, direct.ReasoningTokens},
		{"CacheReadTokens", streamed.CacheReadTokens, direct.CacheReadTokens},
	} {
		switch {
		case (c.streamed == nil) != (c.direct == nil):
			t.Errorf("%s: streaming %v, non-streaming %v", c.name, c.streamed, c.direct)
		case c.streamed != nil && *c.streamed != *c.direct:
			t.Errorf("%s: streaming %d, non-streaming %d", c.name, *c.streamed, *c.direct)
		}
	}
	if usageFromMeta(nil) != nil {
		t.Error("usageFromMeta(nil) should stay nil so callers need no guard")
	}
}
