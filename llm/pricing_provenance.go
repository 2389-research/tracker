// ABOUTME: Records the provider-published pricing each catalog entry was derived from, keyed by model ID.
// ABOUTME: This is the source-of-truth sidecar #518 methodology item 1 asks for; the published-price test asserts the catalog stays consistent with it.
package llm

// PublishedPrice is the provider-published pricing a catalog entry was taken
// from, plus the page it came from. It is a sidecar to ModelInfo — kept out of
// ModelInfo so the runtime pricing struct stays lean — that lets a test assert
// the catalog's constants and cache multipliers have not drifted from their
// documented source of truth (#518 methodology item 1).
//
// A provider states cached-input pricing one of two ways, so the struct carries
// both and the test picks whichever is recorded:
//   - a per-model cached-input dollar price (OpenAI) → CachedInputPerM, from
//     which the expected read multiplier is CachedInputPerM/InputPerM;
//   - a multiplier convention (Anthropic/Gemini publish "cached reads are 0.1x
//     of input") → ReadMult.
type PublishedPrice struct {
	InputPerM  float64 // published per-million input price
	OutputPerM float64 // published per-million output price

	// CachedInputPerM is the provider's published per-model cached-input price,
	// when it publishes one. Zero means the provider states a multiplier
	// convention instead (see ReadMult).
	CachedInputPerM float64

	// ReadMult is the documented cached-read multiplier convention, consulted
	// only when CachedInputPerM is zero. Both zero means no cached-read price is
	// published for this model and the read multiplier is left unasserted.
	ReadMult float64

	// WriteMult is the published per-token cache-WRITE premium convention:
	// Anthropic's 1.25x 5-minute rate; 0 where writes are free (OpenAI prompt
	// cache) or billed as time-based storage rather than per token (Gemini).
	WriteMult float64

	// Source is the pricing page or documented convention the figures came from.
	// Required — a priced model with no source has no drift signal.
	Source string
}

// expectedReadMultiplier resolves the cache-read multiplier the catalog should
// carry for this model from its published pricing: a per-model cached-input
// price yields CachedInputPerM/InputPerM; otherwise the recorded multiplier
// convention. The bool is false when the provider publishes neither, so the
// caller leaves the read multiplier unasserted rather than comparing to zero.
func (p PublishedPrice) expectedReadMultiplier() (float64, bool) {
	if p.CachedInputPerM > 0 && p.InputPerM > 0 {
		return p.CachedInputPerM / p.InputPerM, true
	}
	if p.ReadMult > 0 {
		return p.ReadMult, true
	}
	return 0, false
}

// Source-page shorthands. These are the provider pricing pages the figures were
// read from; the fictional near-future models (gpt-5.x, gemini-3) cite the same
// pages, where their assertion is internal consistency rather than an external
// absolute (see the test).
const (
	srcAnthropic = "anthropic.com/pricing — reads 0.1x, 5-minute writes 1.25x published multipliers"
	srcOpenAI    = "developers.openai.com/api/docs/pricing"
	srcGemini    = "ai.google.dev/gemini-api/docs/pricing — cached reads 0.1x of input; no per-token write"
)

// publishedPrices is the provenance sidecar, keyed by catalog model ID. Every
// catalogued model must have a row (asserted by the published-price test), and
// every row must cite a source. Cache-read pricing is recorded as the provider
// publishes it: OpenAI ships per-model cached-input dollar prices; Anthropic and
// Gemini publish a 0.1x-of-input convention.
var publishedPrices = map[string]PublishedPrice{
	// ── Anthropic ── published as multiplier conventions, not per-model cached
	// dollar prices: reads 0.1x, 5-minute writes 1.25x.
	"claude-opus-4-7":   {InputPerM: 5.0, OutputPerM: 25.0, ReadMult: 0.1, WriteMult: 1.25, Source: srcAnthropic},
	"claude-sonnet-4-6": {InputPerM: 3.0, OutputPerM: 15.0, ReadMult: 0.1, WriteMult: 1.25, Source: srcAnthropic},
	"claude-opus-4-6":   {InputPerM: 5.0, OutputPerM: 25.0, ReadMult: 0.1, WriteMult: 1.25, Source: srcAnthropic},
	"claude-sonnet-4-5": {InputPerM: 3.0, OutputPerM: 15.0, ReadMult: 0.1, WriteMult: 1.25, Source: srcAnthropic},
	"claude-haiku-4-5":  {InputPerM: 1.0, OutputPerM: 5.0, ReadMult: 0.1, WriteMult: 1.25, Source: srcAnthropic},

	// ── OpenAI ── writes free (WriteMult 0). GPT-5.x are near-future models:
	// figures cite the pricing page and the assertion is CachedInputPerM/base
	// internal consistency, not an external absolute.
	"gpt-5.4":       {InputPerM: 2.50, OutputPerM: 15.0, CachedInputPerM: 0.25, Source: srcOpenAI},
	"gpt-5.4-mini":  {InputPerM: 0.75, OutputPerM: 4.50, CachedInputPerM: 0.075, Source: srcOpenAI},
	"gpt-5.4-nano":  {InputPerM: 0.20, OutputPerM: 1.25, CachedInputPerM: 0.02, Source: srcOpenAI},
	"gpt-5.2":       {InputPerM: 5.0, OutputPerM: 15.0, CachedInputPerM: 0.50, Source: srcOpenAI},
	"gpt-5.2-mini":  {InputPerM: 0.30, OutputPerM: 1.20, CachedInputPerM: 0.03, Source: srcOpenAI},
	"gpt-5.2-codex": {InputPerM: 2.50, OutputPerM: 10.0, CachedInputPerM: 0.25, Source: srcOpenAI},
	// GPT-4.1 family: cached input is 0.25x base (a published per-model price).
	"gpt-4.1":      {InputPerM: 2.00, OutputPerM: 8.00, CachedInputPerM: 0.50, Source: srcOpenAI},
	"gpt-4.1-mini": {InputPerM: 0.40, OutputPerM: 1.60, CachedInputPerM: 0.10, Source: srcOpenAI},
	"gpt-4.1-nano": {InputPerM: 0.10, OutputPerM: 0.40, CachedInputPerM: 0.025, Source: srcOpenAI},
	// o-series: cached input 0.1x base.
	"o3":      {InputPerM: 2.00, OutputPerM: 8.00, CachedInputPerM: 0.20, Source: srcOpenAI},
	"o4-mini": {InputPerM: 1.10, OutputPerM: 4.40, CachedInputPerM: 0.11, Source: srcOpenAI},
	// GPT-4o family: cached input 0.5x base.
	"gpt-4o":      {InputPerM: 2.50, OutputPerM: 10.00, CachedInputPerM: 1.25, Source: srcOpenAI},
	"gpt-4o-mini": {InputPerM: 0.15, OutputPerM: 0.60, CachedInputPerM: 0.075, Source: srcOpenAI},

	// ── Gemini ── cached reads 0.1x of input; no per-token write (time-based
	// storage instead). gemini-3.x are near-future previews citing the page.
	"gemini-2.5-pro":         {InputPerM: 1.25, OutputPerM: 10.0, ReadMult: 0.1, Source: srcGemini},
	"gemini-2.5-flash":       {InputPerM: 0.30, OutputPerM: 2.50, ReadMult: 0.1, Source: srcGemini},
	"gemini-2.5-flash-lite":  {InputPerM: 0.10, OutputPerM: 0.40, ReadMult: 0.1, Source: srcGemini},
	"gemini-3.1-pro-preview": {InputPerM: 2.00, OutputPerM: 12.0, ReadMult: 0.1, Source: srcGemini},
	"gemini-3-flash-preview": {InputPerM: 0.50, OutputPerM: 3.0, ReadMult: 0.1, Source: srcGemini},
}
