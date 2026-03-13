// ABOUTME: Per-session cache for tool results, keyed on (tool name, arguments JSON).
// ABOUTME: Supports store, get, invalidateAll, and tracks hit/miss stats.
package agent

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

func (c *toolCache) get(toolName, argsJSON string) (string, bool) {
	key := cacheKey{toolName: toolName, argsJSON: argsJSON}
	if result, ok := c.results[key]; ok {
		c.hits++
		return result, true
	}
	c.misses++
	return "", false
}

func (c *toolCache) store(toolName, argsJSON, result string) {
	key := cacheKey{toolName: toolName, argsJSON: argsJSON}
	c.results[key] = result
}

func (c *toolCache) invalidateAll() {
	clear(c.results)
}
