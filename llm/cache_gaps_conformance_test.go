// ABOUTME: Guards that tracker's cache-rate overlay only ever fills dippin's
// ABOUTME: CacheGaps() — it must never guess a rate for a model dippin prices.
package llm

import (
	"strings"
	"testing"

	"github.com/2389-research/dippin-lang/pricing"
)

// TestCacheOverlayScopeMatchesDippinGaps pins the #571 contract: dippin
// (pricing.CacheGaps()) is authoritative for which priced models still lack a
// cache rate. tracker's overlayCacheMultipliers must (a) be a no-op wherever
// dippin already prices cache, and (b) only ever apply its default to a model in
// CacheGaps(). If dippin adds a cache rate for a model tracker was guessing, or a
// new gap appears, this test forces the overlay's scope to stay in lockstep.
func TestCacheOverlayScopeMatchesDippinGaps(t *testing.T) {
	gaps := map[string]bool{}
	for _, g := range pricing.CacheGaps() {
		// CacheGaps() entries are "provider/model"; key on the model id.
		id := g
		if i := strings.LastIndex(g, "/"); i >= 0 {
			id = g[i+1:]
		}
		gaps[id] = true
	}

	for _, models := range pricing.Providers() {
		for id, mp := range models {
			dippinHasRate := mp.CachedInputPerM > 0 || mp.CacheReadMult > 0

			cp := mp // copy; overlay mutates in place
			overlayCacheMultipliers(&cp, id)

			if dippinHasRate {
				if cp.CacheReadMult != mp.CacheReadMult || cp.CacheWriteMult != mp.CacheWriteMult {
					t.Errorf("%s: dippin prices cache (read=%v) but the overlay changed it (read=%v) — tracker must never guess over dippin",
						id, mp.CacheReadMult, cp.CacheReadMult)
				}
				continue
			}

			// No dippin cache rate: this is a gap. It MUST be in dippin's CacheGaps()
			// (otherwise tracker's field-based guard has drifted from dippin's set),
			// and the overlay must have supplied a default read rate.
			if !gaps[id] {
				t.Errorf("%s: tracker overlays a default cache rate, but the model is NOT in dippin CacheGaps() — the two gap definitions have drifted", id)
			}
			if cp.CacheReadMult == 0 && cp.CachedInputPerM == 0 {
				t.Errorf("%s: gap model got no cache read rate from the overlay", id)
			}
		}
	}
}
