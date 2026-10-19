// ABOUTME: The CLI starts a run with no API key when every agent node is auto,
// ABOUTME: claude-code or acp; --backend auto stays rejected (auto is a node value).
package main

import (
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

func clearProviderEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, k := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "OPENAI_COMPAT_API_KEY", "TRACKER_OPENAI_AUTH", "TRACKER_GATEWAY_URL"} {
		t.Setenv(k, "")
	}
}

func cliGraph(backend string) *pipeline.Graph {
	g := pipeline.NewGraph("cli")
	g.AddNode(&pipeline.Node{ID: "A", Shape: "box", Handler: "codergen", Attrs: map[string]string{"backend": backend}})
	return g
}

func TestResolveLLMClient_AutoGraphNeedsNoKey(t *testing.T) {
	clearProviderEnv(t)
	client, err := resolveLLMClient("", cliGraph("auto"), nil)
	if err != nil || client != nil {
		t.Fatalf("all-auto graph with no keys: client=%v err=%v, want nil, nil", client, err)
	}
}

func TestResolveLLMClient_NativeGraphStillNeedsKey(t *testing.T) {
	clearProviderEnv(t)
	if _, err := resolveLLMClient("", cliGraph("native"), nil); err == nil {
		t.Fatal("a native node with no keys must still fail at startup")
	}
}

func TestCLIBackendAutoRejected(t *testing.T) {
	err := validateBackend("auto")
	if err == nil || !strings.Contains(err.Error(), "auto") {
		t.Fatalf("--backend auto: err = %v, want rejection", err)
	}
}
