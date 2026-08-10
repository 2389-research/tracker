// ABOUTME: Tests the gateway-aware deprecated-model warning in tracker doctor
// ABOUTME: (drives off dippin's ModelPrice.Deprecated).
package tracker

import (
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

func graphPinning(model string) *pipeline.Graph {
	g := pipeline.NewGraph("dep")
	g.AddNode(&pipeline.Node{ID: "A", Shape: "box", Label: "A", Attrs: map[string]string{"llm_model": model}})
	return g
}

func hasDeprecatedWarning(out CheckResult) bool {
	for _, d := range out.Details {
		if d.Status == CheckStatusWarn && contains(d.Message, "retired on the first-party") {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestDoctor_WarnsOnDeprecatedModel: a workflow pinning a model dippin marks
// Deprecated (retired on first-party API) warns when no gateway is configured.
func TestDoctor_WarnsOnDeprecatedModel(t *testing.T) {
	t.Setenv("TRACKER_GATEWAY_URL", "")
	t.Setenv("TRACKER_GATEWAY_KIND", "")
	out := CheckResult{Name: "Pipeline File", Status: CheckStatusOK, Message: "x is valid"}
	appendDeprecatedModelWarnings(&out, graphPinning("claude-opus-4-1")) // deprecated in dippin v0.62
	if !hasDeprecatedWarning(out) {
		t.Fatalf("expected a deprecated-model warning, got details=%v", out.Details)
	}
	if out.Status != CheckStatusWarn {
		t.Fatalf("status should bump to warn, got %q", out.Status)
	}
}

// TestDoctor_SuppressesDeprecatedWarningUnderGateway: with a gateway configured,
// a deprecated model routes to a passthrough platform, so no warning fires.
func TestDoctor_SuppressesDeprecatedWarningUnderGateway(t *testing.T) {
	t.Setenv("TRACKER_GATEWAY_URL", "https://bedrock-gateway.example.workers.dev")
	out := CheckResult{Name: "Pipeline File", Status: CheckStatusOK, Message: "x is valid"}
	appendDeprecatedModelWarnings(&out, graphPinning("claude-opus-4-1"))
	if hasDeprecatedWarning(out) {
		t.Fatalf("gateway configured — deprecated warning should be suppressed, got %v", out.Details)
	}
}

// TestDoctor_CurrentModelNoWarning: a current (non-deprecated) model does not warn.
func TestDoctor_CurrentModelNoWarning(t *testing.T) {
	t.Setenv("TRACKER_GATEWAY_URL", "")
	t.Setenv("TRACKER_GATEWAY_KIND", "")
	out := CheckResult{Name: "Pipeline File", Status: CheckStatusOK, Message: "x is valid"}
	appendDeprecatedModelWarnings(&out, graphPinning("claude-sonnet-4-6"))
	if hasDeprecatedWarning(out) {
		t.Fatalf("current model should not warn, got %v", out.Details)
	}
}
