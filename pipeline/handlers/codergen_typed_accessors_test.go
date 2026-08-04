package handlers

import (
	"os"
	"strings"
	"testing"
)

// TestCodergenRoutesThroughTypedAccessors guards issue #393: the claude-code /
// ACP backend config parsers in codergen.go must read node config through the
// typed AgentNodeConfig accessor, not via raw node.Attrs[...] lookups (see the
// "Typed node-config accessors" gotcha in CLAUDE.md).
//
// Exactly one raw read is intentionally retained: max_budget_usd surfaces a
// parse error on malformed input that the lenient accessor swallows; keeping it
// raw preserves "never silently swallow errors". If that read is ever removed or
// another is added, this test must be revisited deliberately.
func TestCodergenRoutesThroughTypedAccessors(t *testing.T) {
	src, err := os.ReadFile("codergen.go")
	if err != nil {
		t.Fatalf("read codergen.go: %v", err)
	}
	lines := strings.Split(string(src), "\n")

	var raw []string
	for i, line := range lines {
		if strings.Contains(line, "node.Attrs[") {
			raw = append(raw, line)
			if !strings.Contains(line, "max_budget_usd") {
				t.Errorf("line %d: unexpected raw node.Attrs read (route through AgentConfig): %s", i+1, strings.TrimSpace(line))
			}
		}
	}
	if len(raw) != 1 {
		t.Errorf("expected exactly 1 justified raw node.Attrs read (max_budget_usd), found %d:\n%s", len(raw), strings.Join(raw, "\n"))
	}
}
