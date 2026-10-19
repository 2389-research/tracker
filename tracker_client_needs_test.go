// ABOUTME: Engine construction tolerates a missing native client only when no node
// ABOUTME: needs one, and only for "no providers configured"; Backend "auto" is rejected.
package tracker

import (
	"errors"
	"strings"
	"testing"

	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

func needsGraph(backend string) *pipeline.Graph {
	g := pipeline.NewGraph("needs")
	g.AddNode(&pipeline.Node{ID: "A", Shape: "box", Handler: "codergen", Attrs: map[string]string{"backend": backend}})
	return g
}

func TestToleratesMissingClient(t *testing.T) {
	noProviders := &llm.ConfigurationError{SDKError: llm.SDKError{Msg: "no providers configured", Cause: llm.ErrNoProviders}}
	other := &llm.ConfigurationError{SDKError: llm.SDKError{Msg: "invalid TRACKER_OPENAI_AUTH"}}
	if !toleratesMissingClient(Config{}, needsGraph("auto"), noProviders) {
		t.Error("an all-auto graph with no providers must construct")
	}
	if toleratesMissingClient(Config{}, needsGraph("native"), noProviders) {
		t.Error("a graph with a native node must still fail without a client")
	}
	if toleratesMissingClient(Config{}, needsGraph("auto"), other) {
		t.Error("an invalid-config client error must still fail construction")
	}
	if !toleratesMissingClient(Config{Backend: "claude-code"}, needsGraph("native"), other) {
		t.Error("global --backend claude-code keeps today's tolerance of any client error")
	}
}

func TestErrNoProvidersIsWrapped(t *testing.T) {
	_, err := llm.NewClient()
	if !errors.Is(err, llm.ErrNoProviders) {
		t.Fatalf("NewClient with no providers: err = %v, want errors.Is ErrNoProviders", err)
	}
}

func TestConfigBackendAutoRejected(t *testing.T) {
	err := validateConfigBackend("auto")
	if err == nil || !strings.Contains(err.Error(), "node value") {
		t.Fatalf("Config.Backend auto: err = %v, want a rejection explaining it is a node value", err)
	}
	for _, ok := range []string{"", "native", "claude-code", "acp"} {
		if err := validateConfigBackend(ok); err != nil {
			t.Errorf("validateConfigBackend(%q) = %v", ok, err)
		}
	}
}
