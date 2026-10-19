// ABOUTME: tool_access: read_only — the mode parses, the session keeps exactly read /
// ABOUTME: grep_search / glob (never env-gated extras), and the request keeps its tools.
package agent

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/llm"
)

func TestToolAccessMode_Parse(t *testing.T) {
	cases := map[string]ToolAccessMode{
		"":            ToolAccessUnrestricted,
		"  ":          ToolAccessUnrestricted,
		"none":        ToolAccessNone,
		" Read_Only ": ToolAccessReadOnly,
		"read_only":   ToolAccessReadOnly,
		"read-only":   ToolAccessNone, // not the dippin spelling: fail closed
		"junk":        ToolAccessNone,
	}
	for in, want := range cases {
		cfg := DefaultConfig()
		cfg.ToolAccess = in
		if got := cfg.ToolAccessMode(); got != want {
			t.Errorf("ToolAccessMode(%q) = %q, want %q", in, got, want)
		}
		if restricted := cfg.IsToolAccessRestricted(); restricted != (want != ToolAccessUnrestricted) {
			t.Errorf("IsToolAccessRestricted(%q) = %v; read_only must stay restricted for every bypass guard", in, restricted)
		}
	}
}

func registeredToolNames(s *Session) []string {
	var names []string
	for _, d := range s.registry.Definitions() {
		names = append(names, d.Name)
	}
	sort.Strings(names)
	return names
}

func TestReadOnly_RegistersExactlyReadGrepGlob(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ToolAccess = "read_only"
	extras := []*recordingTool{
		{name: "bash"}, {name: "write"}, {name: "generate_code"}, {name: "dispatch_sprints"}, {name: "spawn_agent"},
	}
	sess := mustNewSession(t, &mockCompleter{}, cfg, WithEnvironment(exec.NewLocalEnvironment(t.TempDir())), WithTools(extras[0], extras[1], extras[2], extras[3], extras[4]))
	got := strings.Join(registeredToolNames(sess), ",")
	if got != "glob,grep_search,read" {
		t.Fatalf("read_only registry = %s, want exactly glob,grep_search,read", got)
	}
}

func textResponse(text string) *llm.Response {
	return &llm.Response{
		Message:      llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentPart{{Kind: llm.KindText, Text: text}}},
		FinishReason: llm.FinishReason{Reason: "stop"},
		Usage:        llm.Usage{InputTokens: 5, OutputTokens: 5, TotalTokens: 10},
	}
}

func TestReadOnly_RequestKeepsToolsAndPrompt(t *testing.T) {
	var first *llm.Request
	client := &mockCompleter{
		responses: []*llm.Response{textResponse("the repo does X")},
		onComplete: func(req *llm.Request) {
			if first == nil {
				c := *req
				first = &c
			}
		},
	}
	cfg := DefaultConfig()
	cfg.ToolAccess = "read_only"
	cfg.MaxTurns = 2
	if _, err := mustNewSession(t, client, cfg, WithEnvironment(exec.NewLocalEnvironment(t.TempDir()))).Run(context.Background(), "what does this repo do?"); err != nil {
		t.Fatal(err)
	}
	if first == nil {
		t.Fatal("no request captured")
	}
	if first.ToolChoice != nil && first.ToolChoice.Mode == llm.ToolChoiceNone().Mode {
		t.Fatal("read_only must not force tool_choice none")
	}
	if len(first.Tools) != 3 {
		t.Fatalf("read_only request carries %d tools, want 3", len(first.Tools))
	}
	sys := ""
	for _, m := range first.Messages {
		if m.Role == llm.RoleSystem {
			for _, p := range m.Content {
				sys += p.Text
			}
		}
	}
	if strings.Contains(sys, "No tools are available") {
		t.Fatal("read_only must not get the tool_access=none prompt scrub")
	}
	if strings.Contains(sys, "report_status") {
		t.Fatal("read_only prompt must not tell the model to use report_status, which it does not have")
	}
	if !strings.Contains(sys, "read-only") {
		t.Fatalf("read_only prompt should say the session is read-only; got: %s", sys)
	}
}

func TestReadOnly_WriteCallIsNotExecuted(t *testing.T) {
	writeTool := &recordingTool{name: "write", output: "should-not-execute"}
	client := &mockCompleter{responses: []*llm.Response{{
		Message: llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentPart{{
			Kind:     llm.KindToolCall,
			ToolCall: &llm.ToolCallData{ID: "c1", Name: "write", Arguments: json.RawMessage(`{"path":"x","content":"y"}`)},
		}}},
		FinishReason: llm.FinishReason{Reason: "tool_calls"},
		Usage:        llm.Usage{InputTokens: 5, OutputTokens: 5, TotalTokens: 10},
	}, textResponse("done")}}
	cfg := DefaultConfig()
	cfg.ToolAccess = "read_only"
	cfg.MaxTurns = 3
	if _, err := mustNewSession(t, client, cfg, WithTools(writeTool)).Run(context.Background(), "write a file"); err != nil {
		t.Fatal(err)
	}
	if writeTool.calls != 0 {
		t.Fatalf("write executed %d times under read_only, want 0", writeTool.calls)
	}
}
