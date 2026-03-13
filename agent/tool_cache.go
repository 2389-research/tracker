// ABOUTME: Per-session cache for tool results, keyed on (tool name, canonicalized arguments JSON).
// ABOUTME: Supports store, get, invalidateAll, and tracks hit/miss stats.
package agent

import "bytes"
import "encoding/json"

type cacheKey struct {
	toolName string
	argsJSON string
}

type toolCache struct {
	results map[cacheKey]string
	hits    int
	misses  int
}

func newToolCache() *toolCache {
	return &toolCache{
		results: make(map[cacheKey]string),
	}
}

// compactJSON removes insignificant whitespace from JSON so that
// semantically identical arguments match the same cache key.
func compactJSON(raw string) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(raw)); err != nil {
		return raw
	}
	return buf.String()
}

func (c *toolCache) get(toolName, argsJSON string) (string, bool) {
	key := cacheKey{toolName: toolName, argsJSON: compactJSON(argsJSON)}
	if result, ok := c.results[key]; ok {
		c.hits++
		return result, true
	}
	c.misses++
	return "", false
}

func (c *toolCache) store(toolName, argsJSON, result string) {
	key := cacheKey{toolName: toolName, argsJSON: compactJSON(argsJSON)}
	c.results[key] = result
}

func (c *toolCache) invalidateAll() {
	clear(c.results)
}
