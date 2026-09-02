// Command gen-models renders the supported-model catalog into a static HTML
// partial the website includes on the Models & Providers page.
//
// DRY: models, providers, and prices are ALL derived from the pinned
// dippin-lang/pricing (the single source of truth, #558) — never a second
// hand-maintained list. The output is pure HTML with no Hugo template directives,
// so Hugo includes it verbatim, and it is drift-gated (scripts/docs/gate.sh
// models, wired into pre-commit + CI): a dippin bump that changes prices/models
// fails the gate until the table is regenerated, so the site can never show a
// price or model list that disagrees with the pinned dippin.
//
// Run: make gen-models  (or: go run ./scripts/gen/models)
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/2389-research/dippin-lang/pricing"
)

const defaultOutPath = "site/layouts/partials/models-table.html"

// outPath is the committed partial by default; the drift gate overrides it via
// GEN_MODELS_OUT to render to a temp file and diff without clobbering.
func outPath() string {
	if p := os.Getenv("GEN_MODELS_OUT"); p != "" {
		return p
	}
	return defaultOutPath
}

// providerOrder controls section order + display names. Providers dippin prices
// but that aren't listed here still render (appended, alphabetical) so a new
// dippin provider can't silently vanish from the page.
var providerOrder = []struct{ key, label string }{
	{"anthropic", "Anthropic"},
	{"openai", "OpenAI"},
	{"gemini", "Google (Gemini)"},
	{"deepseek", "DeepSeek"},
	{"grok", "xAI (Grok)"},
	{"mistral", "Mistral"},
	{"cohere", "Cohere"},
	{"zai", "Z.AI (GLM)"},
	{"minimax", "MiniMax"},
	{"moonshot", "Moonshot (Kimi)"},
	{"qwen", "Qwen"},
	{"meta", "Meta (Muse)"},
}

func label(key string) string {
	for _, p := range providerOrder {
		if p.key == key {
			return p.label
		}
	}
	return key
}

// orderedProviders returns provider keys in providerOrder, then any dippin
// provider not in the list (alphabetical), skipping empty ones.
func orderedProviders(provs map[string]map[string]pricing.ModelPrice) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range providerOrder {
		if len(provs[p.key]) > 0 {
			out = append(out, p.key)
			seen[p.key] = true
		}
	}
	var rest []string
	for k, models := range provs {
		if !seen[k] && len(models) > 0 {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

func price(v float64) string {
	if v == 0 {
		return "&mdash;"
	}
	if v < 1 {
		return fmt.Sprintf("$%.3f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

// cachedReadPerM resolves dippin's cached-input rate to an absolute $/M: an
// explicit CachedInputPerM wins; otherwise InputPerM * CacheReadMult; else none.
func cachedReadPerM(mp pricing.ModelPrice) string {
	switch {
	case mp.CachedInputPerM > 0:
		return price(mp.CachedInputPerM)
	case mp.CacheReadMult > 0:
		return price(mp.InputPerM * mp.CacheReadMult)
	default:
		return "&mdash;"
	}
}

func note(mp pricing.ModelPrice) string {
	var n []string
	if !mp.Priced {
		n = append(n, "unpriced")
	}
	if mp.Deprecated {
		n = append(n, "retired (first-party)")
	}
	if len(n) == 0 {
		return "&mdash;"
	}
	return strings.Join(n, "; ")
}

func main() {
	provs := pricing.Providers()

	var b strings.Builder
	b.WriteString("<!-- GENERATED FILE — do not edit by hand.\n")
	b.WriteString("     Source: dippin-lang/pricing (the pinned version) via scripts/gen/models.\n")
	b.WriteString("     Models, providers, and prices are DRY-derived from dippin — run `make gen-models`.\n")
	b.WriteString("     Drift-gated by scripts/docs/gate.sh models (pre-commit + CI). -->\n")

	total := 0
	for _, key := range orderedProviders(provs) {
		models := provs[key]
		ids := make([]string, 0, len(models))
		for id := range models {
			ids = append(ids, id)
		}
		sort.Strings(ids)

		fmt.Fprintf(&b, "<h3 id=\"provider-%s\">%s</h3>\n", key, label(key))
		b.WriteString("<div class=\"table-wrap\">\n")
		b.WriteString("  <table class=\"models-table\">\n")
		b.WriteString("    <thead><tr><th>Model</th><th>Input&nbsp;/M</th><th>Output&nbsp;/M</th><th>Cached&nbsp;read&nbsp;/M</th><th>Notes</th></tr></thead>\n")
		b.WriteString("    <tbody>\n")
		for _, id := range ids {
			mp := models[id]
			fmt.Fprintf(&b,
				"      <tr><td><code>%s</code></td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>\n",
				id, price(mp.InputPerM), price(mp.OutputPerM), cachedReadPerM(mp), note(mp))
			total++
		}
		b.WriteString("    </tbody>\n  </table>\n</div>\n")
	}

	if err := os.WriteFile(outPath(), []byte(b.String()), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "gen-models: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("gen-models: wrote %s (%d models across %d providers)\n", outPath(), total, len(orderedProviders(provs)))
}
