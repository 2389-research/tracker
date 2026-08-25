// ABOUTME: Regression test for #618 — the returned SessionResult must carry the
// ABOUTME: tool-cache hit/miss stats the EventSessionEnd defer finalizes.
package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/2389-research/tracker/agent/tools"
	"github.com/2389-research/tracker/llm"
)

type cacheableStubTool struct{ name, output string }

func (c *cacheableStubTool) Name() string                { return c.name }
func (c *cacheableStubTool) Description() string         { return "cacheable stub tool for testing" }
func (c *cacheableStubTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (c *cacheableStubTool) Execute(context.Context, json.RawMessage) (string, error) {
	return c.output, nil
}
func (c *cacheableStubTool) CachePolicy() tools.CachePolicy { return tools.CachePolicyCacheable }

// TestRun_SurfacesToolCacheStats pins #618: the deferred EventSessionEnd closure
// finalizes ToolCacheHits/Misses onto the RETURNED SessionResult. Before the fix
// Run returned by unnamed value, so the deferred mutation landed on a discarded
// copy and every caller saw 0 — mis-reporting per-session tool-cache telemetry.
func TestRun_SurfacesToolCacheStats(t *testing.T) {
	probe := func(id string) llm.ContentPart {
		return llm.ContentPart{Kind: llm.KindToolCall, ToolCall: &llm.ToolCallData{
			ID: id, Name: "probe", Arguments: json.RawMessage(`{"path":"x.txt"}`),
		}}
	}
	client := &mockCompleter{responses: []*llm.Response{
		{ // turn 1: probe → cache miss + store
			Message:      llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentPart{probe("c1")}},
			FinishReason: llm.FinishReason{Reason: "tool_calls"},
		},
		{ // turn 2: same probe → cache hit
			Message:      llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentPart{probe("c2")}},
			FinishReason: llm.FinishReason{Reason: "tool_calls"},
		},
		{ // turn 3: stop
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}}

	cfg := DefaultConfig()
	cfg.CacheToolResults = true
	sess := mustNewSession(t, client, cfg)
	sess.registry.Register(&cacheableStubTool{name: "probe", output: "probed"})

	res, err := sess.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ToolCacheHits < 1 {
		t.Errorf("ToolCacheHits = %d, want >= 1 — the deferred cache finalize must reach the returned SessionResult (#618)", res.ToolCacheHits)
	}
	if res.ToolCacheMisses < 1 {
		t.Errorf("ToolCacheMisses = %d, want >= 1", res.ToolCacheMisses)
	}
}
