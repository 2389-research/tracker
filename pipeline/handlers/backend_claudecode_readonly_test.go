// ABOUTME: tool_access: read_only on claude-code limits the built-in tool set itself and
// ABOUTME: refuses anything unlisted; params bypasses are ignored; ACP refuses to start.
package handlers

import (
	"slices"
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

func readOnlyNode(attrs map[string]string) *pipeline.Node {
	a := map[string]string{"tool_access": "read_only"}
	for k, v := range attrs {
		a[k] = v
	}
	return &pipeline.Node{ID: "Answer", Shape: "box", Attrs: a}
}

// argvTail returns the args after the fixed --print … --output-format prefix.
func readOnlyArgv(t *testing.T, node *pipeline.Node) []string {
	t.Helper()
	cc, err := NewCodergenHandler(nil, t.TempDir()).buildClaudeCodeConfig(node)
	if err != nil {
		t.Fatalf("buildClaudeCodeConfig: %v", err)
	}
	args, err := buildArgs(pipeline.AgentRunConfig{Prompt: "p", ToolAccess: node.Attrs["tool_access"], Extra: cc})
	if err != nil {
		t.Fatalf("buildArgs: %v", err)
	}
	i := slices.Index(args, "stream-json")
	return args[i+1:]
}

func wantReadOnlyArgv() []string {
	deny := []string{}
	for _, tool := range canonicalClaudeCodeToolDenyList {
		if tool != "Read" && tool != "Glob" && tool != "Grep" {
			deny = append(deny, tool)
		}
	}
	return []string{
		"--permission-mode", "dontAsk",
		"--allowedTools", "Read,Glob,Grep",
		"--disallowedTools", strings.Join(deny, ","),
		"--tools", "Read,Glob,Grep",
		"--strict-mcp-config",
		"--setting-sources", "user",
	}
}

func TestClaudeCodeReadOnlyArgv(t *testing.T) {
	got := readOnlyArgv(t, readOnlyNode(nil))
	if !slices.Equal(got, wantReadOnlyArgv()) {
		t.Fatalf("read_only argv:\n got  %q\n want %q", got, wantReadOnlyArgv())
	}
}

func TestClaudeCodeReadOnlyIgnoresParamsBypass(t *testing.T) {
	node := readOnlyNode(map[string]string{
		"permission_mode": "bypassPermissions",
		"allowed_tools":   "Bash",
		"mcp_servers":     "fs=npx @modelcontextprotocol/server-filesystem /",
	})
	if got := readOnlyArgv(t, node); !slices.Equal(got, wantReadOnlyArgv()) {
		t.Fatalf("read_only must ignore permission_mode / allowed_tools / mcp_servers:\n got  %q\n want %q", got, wantReadOnlyArgv())
	}
}

func TestClaudeCodeReadOnlyDirectRunArgv(t *testing.T) {
	// A direct caller with ToolAccess set but no ClaudeCodeConfig still gets
	// the full read_only surface (the #259 direct-Run bypass shape).
	args, err := buildArgs(pipeline.AgentRunConfig{Prompt: "p", ToolAccess: "read_only"})
	if err != nil {
		t.Fatal(err)
	}
	i := slices.Index(args, "stream-json")
	want := wantReadOnlyArgv()
	if got := args[i+1:]; !slices.Equal(got, want) {
		t.Fatalf("direct-Run read_only argv:\n got  %q\n want %q", got, want)
	}
}

func TestClaudeCodeReadOnlyRefusalsExpected(t *testing.T) {
	if !refusalsExpected(pipeline.AgentRunConfig{ToolAccess: "read_only"}) {
		t.Fatal("a read_only node asked for refusals; one must not fail it (#676)")
	}
}

func TestClaudeCodeNoneUnchanged(t *testing.T) {
	cc, err := NewCodergenHandler(nil, t.TempDir()).buildClaudeCodeConfig(&pipeline.Node{ID: "N", Attrs: map[string]string{"tool_access": "none"}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cc.DisallowedTools, canonicalClaudeCodeToolDenyList) || len(cc.Tools) != 0 || cc.StrictMCPConfig {
		t.Fatalf("tool_access none must keep today's full deny list only: %+v", cc)
	}
}

func TestACPReadOnlyRefusedBeforeStart(t *testing.T) {
	kind, err := refuseOnUnsupportedBackend(readOnlyNode(nil), &ACPBackend{})
	if err == nil || kind != refuseReadOnlyACP {
		t.Fatalf("ACP + read_only: kind=%q err=%v, want read_only_acp refusal", kind, err)
	}
	if kind, err := refuseOnUnsupportedBackend(readOnlyNode(nil), &NativeBackend{}); err != nil {
		t.Fatalf("native + read_only must start: kind=%q err=%v", kind, err)
	}
	var jailErr error
	_, jailErr = refuseOnUnsupportedBackend(&pipeline.Node{ID: "J", Attrs: map[string]string{"writable_paths": "src/**"}}, &ACPBackend{})
	if jailErr == nil {
		t.Fatal("writable_paths on ACP must still refuse")
	}
}
